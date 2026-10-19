package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Public API -----------------------------------------------------------------

// TestInfraGCS exercises the real CLI, a local template and a disposable GCS bucket.
func TestInfraGCS(t *testing.T) {
	if os.Getenv("PREMISE_TEST_GCS") != "1" {
		t.Skip("set PREMISE_TEST_GCS=1 to create and destroy a live test bucket")
	}
	for _, key := range []string{"GCP_PROJECT_ID", "PULUMI_BACKEND_URL"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s is required", key)
		}
	}
	if !strings.HasPrefix(os.Getenv("PULUMI_BACKEND_URL"), "gs://") {
		t.Fatal("PULUMI_BACKEND_URL must select an existing GCS state bucket")
	}
	for _, tool := range []string{"mise", "pulumi", "gcloud"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	binary := os.Getenv("PREMISE_BIN")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "pm")
		gcsCommand(t, repositoryRoot(t), "go", "build", "-o", binary, ".")
	}
	t.Setenv("PULUMI_HOME", t.TempDir())
	t.Setenv("PULUMI_CONFIG_PASSPHRASE", "")
	fixture := filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi")
	client := filepath.Join(t.TempDir(), "client")
	copyGCSFixture(t, filepath.Join(fixture, "client"), client)
	gcsCommand(t, client, binary, "g", filepath.Join(fixture, "registry")+":bucket", "--answer", "name=environment")
	gcsCommand(t, client, binary, "infra", "bind", "dummy", "environment", "--artifact", "artifact")
	gcsCommand(t, client, binary, "infra", "test", "environment")

	branch := fmt.Sprintf("feature/gcs-%d", time.Now().UnixNano())
	scope := []string{"--env", "preview", "--branch", branch}
	infra := func(operation string, args ...string) string {
		arguments := append([]string{"infra", operation}, scope...)
		return gcsCommand(t, client, binary, append(arguments, args...)...)
	}
	infra("init", "--secrets-provider", "passphrase")
	var bucket string
	t.Cleanup(func() {
		// Cleanup has its own context so a failed assertion cannot cancel destruction.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		// Merge cleanup may be followed by the same branch's deletion event.
		for attempt := 1; attempt <= 2; attempt++ {
			command := exec.CommandContext(ctx, binary, append([]string{"infra", "destroy"}, scope...)...)
			command.Dir = client
			if output, err := command.CombinedOutput(); err != nil {
				t.Errorf("cleanup attempt %d failed for branch %s, bucket %s: %v\n%s", attempt, branch, bucket, err, output)
				return
			}
		}
		if bucket != "" {
			check := exec.CommandContext(ctx, "gcloud", "storage", "buckets", "describe", "gs://"+bucket)
			if output, err := check.CombinedOutput(); err == nil || !strings.Contains(string(output), "404") {
				t.Errorf("could not confirm bucket %s was deleted: %v\n%s", bucket, err, output)
			}
		}
	})
	infra("plan")
	infra("apply", "--yes")
	outputs := map[string]string{}
	if err := json.Unmarshal([]byte(infra("output")), &outputs); err != nil {
		t.Fatal(err)
	}
	bucket = outputs["bucketName"]
	if bucket == "" || outputs["objectName"] != "hello.txt" {
		t.Fatalf("unexpected stack outputs: %v", outputs)
	}
	object := "gs://" + bucket + "/" + outputs["objectName"]
	if got := gcsCommand(t, client, "gcloud", "storage", "cat", object); got != "Hello from the Premise GCS fixture.\n" {
		t.Fatalf("bound artifact content = %q", got)
	}
	infra("apply", "--yes")

	// A fresh source directory must recover the same remote stack and bucket.
	fresh := filepath.Join(t.TempDir(), "client")
	copyGCSFixture(t, client, fresh)
	gcsCommand(t, fresh, binary, append([]string{"infra", "init"}, scope...)...)
	var recovered map[string]string
	if err := json.Unmarshal([]byte(gcsCommand(t, fresh, binary, append([]string{"infra", "output"}, scope...)...)), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered["bucketName"] != bucket {
		t.Fatalf("fresh client recovered %q, want %q", recovered["bucketName"], bucket)
	}
	gcsCommand(t, fresh, binary, append([]string{"infra", "plan"}, scope...)...)
	t.Logf("Verified generation, binding, preview/apply/reapply, object contents and remote state: %s", object)
}

// Private helpers ------------------------------------------------------------

func gcsCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	// Keep JSON stdout separate from Pulumi's progress and diagnostic output.
	var diagnostics strings.Builder
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s\n%s", name, strings.Join(arguments, " "), err, output, diagnostics.String())
	}
	return string(output)
}

func copyGCSFixture(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Copy source only; stack files and local Pulumi state must not travel.
		if entry.Name() == ".pulumi" || (strings.HasPrefix(entry.Name(), "Pulumi.") && entry.Name() != "Pulumi.yaml") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
