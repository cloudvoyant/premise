package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestInfra(t *testing.T) {
	requireLiveInfrastructure(t)
	binary := premiseBinary(t)
	t.Setenv("PULUMI_HOME", t.TempDir())

	t.Run("when setting up infra", func(t *testing.T) {
		// Arrange.
		client := newLiveClient(t, binary)

		// Act.
		client.infra(t, "setup", "dev")

		// Assert.
		t.Run("it installs the pinned Pulumi tools and program dependencies", func(t *testing.T) {
			version := runCommand(t, client.root, "mise", "exec", "--", "pulumi", "version")
			if !strings.Contains(version, "v3.268.0") {
				t.Fatalf("unexpected Pulumi version: %s", version)
			}
			assertFileContains(t, filepath.Join(client.root, "infra/go.mod"), "pulumi-gcp")
		})
		t.Run("it prepares state without provisioning resources", func(t *testing.T) {
			if strings.TrimSpace(client.infra(t, "exists", "dev")) != "false" {
				t.Fatal("setup provisioned resources")
			}
		})
	})

	t.Run("when planning infra", func(t *testing.T) {
		// Arrange.
		client := newLiveClient(t, binary)
		client.infra(t, "setup", "dev")

		// Act.
		plan := client.infra(t, "plan", "dev")

		// Assert.
		t.Run("it previews the bucket creation", func(t *testing.T) {
			if !strings.Contains(plan, "gcp:storage:Bucket") {
				t.Fatalf("plan did not describe the bucket: %s", plan)
			}
		})
		t.Run("it leaves the resource state unchanged", func(t *testing.T) {
			if strings.TrimSpace(client.infra(t, "exists", "dev")) != "false" {
				t.Fatal("plan applied resources")
			}
		})
	})

	t.Run("when applying infra updates", func(t *testing.T) {
		client := newLiveClient(t, binary)
		for _, environment := range []string{"dev", "stage", "prod", "preview"} {
			t.Run("for "+environment, func(t *testing.T) {
				// Arrange.
				t.Setenv("PREMISE_TEST_REVISION", "a")
				client.infra(t, "setup", environment)
				client.infra(t, "apply", environment, "--yes")
				bucket := client.bucket(t, environment)
				t.Setenv("PREMISE_TEST_REVISION", "b")

				// Act.
				client.infra(t, "apply", environment, "--yes")

				// Assert.
				t.Run("it updates the existing bucket", func(t *testing.T) {
					if got := client.bucket(t, environment); got != bucket {
						t.Fatalf("update replaced bucket %s with %s", bucket, got)
					}
					revision := runCommand(t, client.root, "gcloud", "storage", "buckets", "describe", "gs://"+bucket, "--format=value(labels.revision)")
					if strings.TrimSpace(revision) != "b" {
						t.Fatalf("bucket revision = %q", revision)
					}
				})
				t.Run("it saves state in the selected remote backend", func(t *testing.T) { client.assertSavedBackend(t, environment) })
			})
		}

		t.Run("when the program fails", func(t *testing.T) {
			// Arrange.
			client := newLiveClient(t, binary)
			client.infra(t, "setup", "dev")
			client.infra(t, "apply", "dev", "--yes")
			bucket := client.bucket(t, "dev")
			t.Setenv("PREMISE_TEST_FAIL_INFRA", "1")
			args := append([]string{"infra", "apply"}, client.scope("dev")...)

			// Act.
			failure := commandFailure(t, client.root, binary, append(args, "--yes")...)

			// Assert.
			t.Run("it returns the infrastructure failure", func(t *testing.T) {
				if !strings.Contains(failure, "requested infrastructure failure") {
					t.Fatalf("wrong failure: %s", failure)
				}
			})
			t.Run("it preserves the provisioned bucket for correction", func(t *testing.T) {
				if got := client.bucket(t, "dev"); got != bucket {
					t.Fatalf("saved bucket changed: %s", got)
				}
				runCommand(t, client.root, "gcloud", "storage", "buckets", "describe", "gs://"+bucket)
			})
		})
	})

	t.Run("when querying infra", func(t *testing.T) {
		// Arrange.
		client := newLiveClient(t, binary)
		client.infra(t, "setup", "stage")
		client.infra(t, "apply", "stage", "--yes")
		bucket := client.bucket(t, "stage")

		t.Run("it returns exported stack outputs", func(t *testing.T) {
			output := client.infra(t, "query", "stage", "--output", "bucketName")
			if strings.TrimSpace(output) != bucket {
				t.Fatalf("wrong bucket output: %s", output)
			}
		})
		t.Run("it returns saved resource properties", func(t *testing.T) {
			output := client.infra(t, "query", "stage", "--resource", "storage", "--property", "name")
			if strings.TrimSpace(output) != bucket {
				t.Fatalf("wrong bucket property: %s", output)
			}
		})
		t.Run("it reuses remote state from a fresh checkout", func(t *testing.T) {
			fresh := filepath.Join(t.TempDir(), "client")
			copyFixture(t, client.root, fresh)
			output := runCommand(t, fresh, binary, "infra", "query", "--env", "stage", "--output", "bucketName")
			if strings.TrimSpace(output) != bucket {
				t.Fatalf("fresh checkout read %q", output)
			}
		})
	})

	t.Run("when destroying infra", func(t *testing.T) {
		// Arrange.
		client := newLiveClient(t, binary)
		client.infra(t, "setup", "preview")
		client.infra(t, "apply", "preview", "--yes")
		bucket := client.bucket(t, "preview")

		// Act.
		client.infra(t, "destroy", "preview", "--yes")

		// Assert.
		t.Run("it deletes the preview bucket", func(t *testing.T) { client.assertBucketDeleted(t, bucket) })
		t.Run("it succeeds when destruction runs again", func(t *testing.T) { client.infra(t, "destroy", "preview", "--yes") })
	})
}
