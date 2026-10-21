package e2e_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

func TestInfraGCSCreatesIsolatedBucketsAndPreservesOtherPreviewOnCleanup(t *testing.T) {
	// Arrange.
	if os.Getenv("PREMISE_TEST_GCS") != "1" {
		t.Skip("set PREMISE_TEST_GCS=1 with an explicit GCP test project and GCS backend")
	}
	t.Setenv("PULUMI_HOME", t.TempDir())
	project := os.Getenv("PREMISE_TEST_GCP_PROJECT")
	backend := os.Getenv("PULUMI_BACKEND_URL")
	if project == "" || !strings.HasPrefix(backend, "gs://") {
		t.Fatal("PREMISE_TEST_GCP_PROJECT and an existing gs:// PULUMI_BACKEND_URL are required")
	}
	for _, tool := range []string{"mise", "gcloud"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	if actual := strings.TrimSpace(infraCommand(t, repositoryRoot(t), "gcloud", "projects", "describe", project, "--format=value(projectId)")); actual != project {
		t.Fatalf("GCP project %q does not match explicitly selected test project %q", actual, project)
	}
	binary := premiseBinary(t)
	client := filepath.Join(t.TempDir(), "client")
	copyInfraFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/client"), client)
	manifest, err := core.LoadManifest(filepath.Join(client, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Infra.Environments = map[string]core.InfraEnvironment{
		"preview": {Config: map[string]string{"gcp:project": project}},
	}
	if err := core.SaveManifest(filepath.Join(client, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	infraCommand(t, client, "mise", "run", "infra:install")
	branches := []string{fmt.Sprintf("feature/gcs-a-%d", time.Now().UnixNano()), fmt.Sprintf("feature/gcs-b-%d", time.Now().UnixNano())}
	buckets := make([]string, len(branches))
	operation := func(root, branch, name string, args ...string) string {
		arguments := []string{"infra", name, "--env", "preview", "--branch", branch}
		return infraCommand(t, root, binary, append(arguments, args...)...)
	}
	t.Cleanup(func() {
		for i, branch := range branches {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			for attempt := 0; attempt < 2; attempt++ {
				command := exec.CommandContext(ctx, binary, "infra", "destroy", "--env", "preview", "--branch", branch)
				command.Dir = client
				command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client)
				if output, err := command.CombinedOutput(); err != nil {
					t.Errorf("cleanup %s: %v\n%s", branch, err, output)
					break
				}
			}
			if buckets[i] != "" {
				check := exec.CommandContext(ctx, "gcloud", "storage", "buckets", "describe", "gs://"+buckets[i])
				if output, err := check.CombinedOutput(); err == nil || !strings.Contains(string(output), "404") {
					t.Errorf("could not verify deletion of %s: %v\n%s", buckets[i], err, output)
				}
			}
			cancel()
		}
	})

	// Act.
	for i, branch := range branches {
		operation(client, branch, "init")
		operation(client, branch, "plan")
		operation(client, branch, "apply", "--yes")
		buckets[i] = strings.TrimSpace(operation(client, branch, "query", "--output", "bucketName"))
		id := strings.TrimSpace(operation(client, branch, "query", "--resource", "storage", "--property", "id"))
		if buckets[i] == "" || id != buckets[i] {
			t.Fatalf("unexpected bucket/ID: %q %q", buckets[i], id)
		}
		if exported := strings.TrimSpace(operation(client, branch, "query", "--output", "bucketUrl")); exported != "gs://"+buckets[i] {
			t.Fatalf("unexpected bucket URL: %q", exported)
		}
		infraCommand(t, client, "gcloud", "storage", "buckets", "describe", "gs://"+buckets[i])
	}
	operation(client, branches[0], "apply", "--yes")
	fresh := filepath.Join(t.TempDir(), "client")
	copyInfraFixture(t, client, fresh)
	operation(fresh, branches[0], "init")
	recovered := strings.TrimSpace(operation(fresh, branches[0], "query", "--output", "bucketName"))
	operation(fresh, branches[0], "plan")
	operation(client, branches[0], "destroy")

	// Assert.
	if buckets[0] == buckets[1] {
		t.Fatal("different preview stacks share a bucket")
	}
	if recovered != buckets[0] {
		t.Fatalf("fresh checkout recovered %q, want %q", recovered, buckets[0])
	}
	infraCommand(t, client, "gcloud", "storage", "buckets", "describe", "gs://"+buckets[1])
	t.Logf("Verified real GCS buckets, resource queries, remote state, and preview cleanup isolation: %s, %s", buckets[0], buckets[1])
}

// Private helpers ------------------------------------------------------------

func premiseBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("PREMISE_BIN"); binary != "" {
		resolved, err := exec.LookPath(binary)
		if err != nil {
			t.Fatal(err)
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			t.Fatal(err)
		}
		return absolute
	}
	binary := filepath.Join(t.TempDir(), "pm")
	infraCommand(t, repositoryRoot(t), "go", "build", "-o", binary, ".")
	return binary
}

func infraCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+directory)
	var diagnostics strings.Builder
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s\n%s", name, strings.Join(arguments, " "), err, output, diagnostics.String())
	}
	return string(output)
}

func copyInfraFixture(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == ".pulumi" || entry.Name() == "node_modules" || (strings.HasPrefix(entry.Name(), "Pulumi.") && entry.Name() != "Pulumi.yaml") {
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
			return os.MkdirAll(target, 0755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("fixture contains a non-regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}
