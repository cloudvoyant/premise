package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

func TestCI(t *testing.T) {
	binary := premiseBinary(t)
	t.Setenv("PULUMI_HOME", t.TempDir())

	t.Run("when a user commits on a non-main branch", func(t *testing.T) {
		// Arrange.
		root := ciProject(t)
		git(t, root, "switch", "-c", "feature/example")
		commitChange(t, root, "feat: add static page [publish-rc]")
		t.Setenv("GITHUB_EVENT_NAME", "push")
		t.Setenv("GITHUB_REF", "refs/heads/feature/example")
		version := strings.TrimSpace(runCommand(t, root, binary, "version", "rc", "--identifier", "1"))
		artifacts := t.TempDir()
		events := filepath.Join(t.TempDir(), "events")
		t.Setenv("PREMISE_TEST_EVENTS", events)
		t.Setenv("PREMISE_ARTIFACT_DIR", artifacts)
		t.Setenv("PREMISE_RELEASE_CHANNEL", "rc")
		t.Setenv("RELEASE_VERSION", strings.TrimPrefix(version, "v"))

		// Act: the hosted workflow stages files before its separate publication job.
		stageCIArtifacts(t, binary, root, "on-commit", "rc", version, artifacts)

		// Assert.
		t.Run("it runs tests formatting and linting before release staging", func(t *testing.T) {
			assertFileContent(t, events, "build::A\ntest::A\nformat:check::A\nlint::A\nrelease:build::A\n")
		})
		t.Run("it creates RC artifacts for publication", func(t *testing.T) {
			assertFileContains(t, filepath.Join(artifacts, "index.html"), "Release A")
			assertFileContent(t, filepath.Join(artifacts, "version.txt"), "rc:1.1.0-rc.1\n")
		})
		t.Run("it schedules optional migration work for the app", func(t *testing.T) {
			plan := runCommand(t, root, binary, "ci", "plan", "--ci-output")
			if !strings.Contains(plan, "has_migrations=true\n") || !strings.Contains(plan, "migration_matrix=") {
				t.Fatalf("missing migration schedule: %s", plan)
			}
		})

		t.Run("when a check fails", func(t *testing.T) {
			// Arrange.
			t.Setenv("PREMISE_TEST_FAIL_TASK", "test")
			failedArtifacts := t.TempDir()
			t.Setenv("PREMISE_ARTIFACT_DIR", failedArtifacts)

			// Act.
			failure := failCIArtifacts(t, binary, root, "on-commit", "rc", version, failedArtifacts)

			// Assert.
			t.Run("it stops before creating release artifacts", func(t *testing.T) {
				if !strings.Contains(failure, "requested test failure") {
					t.Fatalf("wrong check failure: %s", failure)
				}
				assertAbsent(t, filepath.Join(failedArtifacts, "index.html"))
			})
		})
		t.Run("when RC artifact creation fails", func(t *testing.T) {
			// Arrange.
			t.Setenv("PREMISE_TEST_FAIL_TASK", "release:build")
			failedArtifacts := t.TempDir()

			// Act.
			failure := failCIArtifacts(t, binary, root, "on-commit", "rc", version, failedArtifacts)

			// Assert.
			t.Run("it fails without creating a release tag", func(t *testing.T) {
				if !strings.Contains(failure, "stage native artifacts") {
					t.Fatalf("wrong artifact failure: %s", failure)
				}
				if tags := gitOutput(t, root, "tag", "--list"); tags != "v1.0.0" {
					t.Fatalf("unexpected tags after failure: %s", tags)
				}
			})
		})
		t.Run("when deploying the branch preview", func(t *testing.T) {
			// Arrange and act.
			client := newLiveClient(t, binary)
			git(t, client.root, "switch", "-c", client.branch)
			client.flow(t, "on-commit", "preview", "A")

			// Assert.
			t.Run("it creates a preview environment for the branch", func(t *testing.T) {
				client.bucket(t, "preview")
				client.assertSavedBackend(t, "preview")
			})
			t.Run("it runs preview migrations", func(t *testing.T) { assertMigration(t, client, "preview", "A") })
			t.Run("it runs non-IaC preview deployment hooks", func(t *testing.T) { assertDeployedRelease(t, client, "preview", "A") })
			t.Run("it combines JSON secrets with queried preview outputs", func(t *testing.T) {
				runCommand(t, client.root, binary, "secrets", "pull", "--project", "web", "--env", "preview", "--branch", client.branch)
				destination := filepath.Join(client.root, "apps/web/.env.local")
				assertFileContains(t, destination, "APP_MESSAGE=\"preview-example\"")
				assertFileContains(t, destination, "BUCKET_NAME=\""+client.bucket(t, "preview")+"\"")
			})
			t.Run("when the new app fails its health check", func(t *testing.T) {
				// Arrange.
				t.Setenv("PM_RELEASE", "B")
				t.Setenv("PREMISE_TEST_FAIL_HEALTH", "preview")

				// Act.
				failure := commandFailure(t, client.root, binary, "ci", "flow", "on-deploy", "--environment", "preview", "--branch", client.branch)

				// Assert.
				t.Run("it reports the failed B health check", func(t *testing.T) {
					if !strings.Contains(failure, "health") || !strings.Contains(failure, "<h1>Release B</h1>") {
						t.Fatalf("unexpected failure: %s", failure)
					}
				})
				t.Run("it restores the previous app using native object history", func(t *testing.T) { assertDeployedRelease(t, client, "preview", "A") })
				t.Run("it preserves the migration that already ran", func(t *testing.T) { assertMigration(t, client, "preview", "B") })
			})
		})
	})

	t.Run("when a user merges to main", func(t *testing.T) {
		// Arrange.
		root := ciProject(t)
		commitChange(t, root, "feat: add stable page")
		version := strings.TrimSpace(runCommand(t, root, binary, "version", "next"))
		artifacts := t.TempDir()

		// Act.
		stageCIArtifacts(t, binary, root, "on-merge", "stable", version, artifacts)

		// Assert.
		t.Run("it bumps the version from conventional commits", func(t *testing.T) {
			if version != "v1.1.0" {
				t.Fatalf("next version = %q", version)
			}
		})
		t.Run("it creates stable release artifacts for publication", func(t *testing.T) {
			assertFileContains(t, filepath.Join(artifacts, "index.html"), "Release A")
			assertFileContent(t, filepath.Join(artifacts, "version.txt"), "stable:1.1.0\n")
		})
		t.Run("when updating environments after publication", func(t *testing.T) {
			// Arrange.
			client := newLiveClient(t, binary)
			client.flow(t, "on-commit", "preview", "A")
			previewBucket := client.bucket(t, "preview")
			client.flow(t, "on-deploy", "dev", "A")
			devBucket := client.bucket(t, "dev")

			// Act.
			client.flow(t, "on-merge", "preview", "A")
			client.flow(t, "on-merge", "dev", "B")

			// Assert.
			t.Run("it destroys the merged preview environment", func(t *testing.T) { client.assertBucketDeleted(t, previewBucket) })
			t.Run("it succeeds when preview cleanup runs again", func(t *testing.T) { client.flow(t, "on-merge", "preview", "A") })
			t.Run("it updates the existing dev environment", func(t *testing.T) {
				if got := client.bucket(t, "dev"); got != devBucket {
					t.Fatalf("dev bucket changed: %s", got)
				}
			})
			t.Run("it runs dev migrations", func(t *testing.T) { assertMigration(t, client, "dev", "B") })
			t.Run("it runs non-IaC dev deployment hooks", func(t *testing.T) { assertDeployedRelease(t, client, "dev", "B") })
		})
	})

	t.Run("when a user deploys to dev stage or prod", func(t *testing.T) {
		requireLiveInfrastructure(t)
		// Arrange: build one release artifact and promote those same bytes to each environment.
		root := ciProject(t)
		artifacts := t.TempDir()
		t.Setenv("PM_RELEASE", "B")
		stageCIArtifacts(t, binary, root, "on-merge", "stable", "v1.1.0", artifacts)
		client := newLiveClient(t, binary)
		copyFixture(t, artifacts, filepath.Join(client.root, "apps/web/releases/B"))

		for _, environment := range []string{"dev", "stage", "prod"} {
			t.Run("to "+environment, func(t *testing.T) {
				// Arrange.
				client.flow(t, "on-deploy", environment, "A")
				bucket := client.bucket(t, environment)
				events := filepath.Join(t.TempDir(), "events")
				t.Setenv("PREMISE_TEST_EVENTS", events)

				// Act.
				client.flow(t, "on-deploy", environment, "B")

				// Assert.
				t.Run("it promotes the same artifact to the selected environment", func(t *testing.T) {
					if got := client.object(t, environment, "index.html"); got != readFile(t, filepath.Join(artifacts, "index.html")) {
						t.Fatal("deployed bytes differ from the promoted artifact")
					}
					if got := client.bucket(t, environment); got != bucket {
						t.Fatal("deployment replaced the environment bucket")
					}
				})
				t.Run("it runs the corresponding migration and deployment", func(t *testing.T) {
					assertMigration(t, client, environment, "B")
					assertFileContent(t, events, "migrate:"+environment+":B\ndeploy:"+environment+":B\nhealth:"+environment+":B\n")
				})
				t.Run("it uses the environment's configured backend", func(t *testing.T) { client.assertSavedBackend(t, environment) })
			})
		}
	})
}

// Private helpers ------------------------------------------------------------

func ciProject(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "client")
	copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/client"), root)
	writeFixtureFile(t, filepath.Join(root, "mise.toml"), "monorepo_root = true\n[monorepo]\nconfig_roots = ['apps/*', 'libs/*']\n", 0644)
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	platform, err := core.HostCIPlatform()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Projects[0].CI.ReleasePlatforms = []string{platform}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PM_RELEASE", "A")
	git(t, root, "init", "--quiet", "--initial-branch=main")
	git(t, root, "config", "user.email", "test@example.invalid")
	git(t, root, "config", "user.name", "Premise E2E")
	git(t, root, "add", ".")
	git(t, root, "commit", "--quiet", "-m", "chore: initial project")
	git(t, root, "tag", "v1.0.0")
	return root
}

func stageCIArtifacts(t *testing.T, binary, root, flow, channel, version, destination string) {
	t.Helper()
	platform, err := core.HostCIPlatform()
	if err != nil {
		t.Fatal(err)
	}
	runCommand(t, root, binary, "ci", "flow", flow, "--release", "none", "--project", "web", "--platform", platform,
		"--channel", channel, "--version", version, "--output-dir", destination)
}

func assertDeployedRelease(t *testing.T, client *liveClient, environment, release string) {
	t.Helper()
	if page := client.object(t, environment, "index.html"); !strings.Contains(page, "<h1>Release "+release+"</h1>") {
		t.Fatalf("wrong %s release: %s", environment, page)
	}
}

func assertMigration(t *testing.T, client *liveClient, environment, release string) {
	t.Helper()
	if marker := strings.TrimSpace(client.object(t, environment, "schema-version.txt")); marker != environment+":"+release {
		t.Fatalf("wrong migration marker: %s", marker)
	}
}

func commitChange(t *testing.T, root, message string) {
	t.Helper()
	writeFixtureFile(t, filepath.Join(root, "CHANGE.txt"), message+"\n", 0644)
	git(t, root, "add", "CHANGE.txt")
	git(t, root, "commit", "--quiet", "-m", message)
}

func failCIArtifacts(t *testing.T, binary, root, flow, channel, version, destination string) string {
	t.Helper()
	platform, err := core.HostCIPlatform()
	if err != nil {
		t.Fatal(err)
	}
	return commandFailure(t, root, binary, "ci", "flow", flow, "--release", "none", "--project", "web", "--platform", platform,
		"--channel", channel, "--version", version, "--output-dir", destination)
}
