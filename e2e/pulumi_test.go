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

func TestInfraGCSCIFlowsDeployAcrossEnvironmentsAndRestorePreviousObjectVersion(t *testing.T) {
	// Arrange.
	if os.Getenv("PREMISE_TEST_GCS") != "1" {
		t.Skip("set PREMISE_TEST_GCS=1 with GCP credentials and a remote backend")
	}
	project := os.Getenv("PREMISE_TEST_GCP_PROJECT")
	if project == "" || !strings.HasPrefix(os.Getenv("PULUMI_BACKEND_URL"), "gs://") {
		t.Fatal("select PREMISE_TEST_GCP_PROJECT and an existing gs:// PULUMI_BACKEND_URL")
	}
	t.Setenv("PULUMI_HOME", t.TempDir())
	t.Setenv("PULUMI_CONFIG_PASSPHRASE", "")
	binary := premiseBinary(t)
	bin := t.TempDir()
	if err := os.Symlink(binary, filepath.Join(bin, "pm")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	client := filepath.Join(t.TempDir(), "client")
	copyInfraFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/client"), client)
	manifest, err := core.LoadManifest(filepath.Join(client, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	environments := []string{"dev", "stage", "prod", "preview"}
	backend := strings.TrimRight(os.Getenv("PULUMI_BACKEND_URL"), "/") + fmt.Sprintf("/run-%d", time.Now().UnixNano())
	manifest.Workspace.Infra.Backend = "primary"
	manifest.Workspace.Infra.Backends = map[string]core.InfraBackend{
		"primary":   {URL: backend},
		"alternate": {URL: backend + "/alternate"},
	}
	manifest.Workspace.Infra.Environments = map[string]core.InfraEnvironment{}
	for _, environment := range environments {
		selected := "primary"
		if environment == "prod" || environment == "preview" {
			selected = "alternate"
		}
		manifest.Workspace.Infra.Environments[environment] = core.InfraEnvironment{Backend: selected, Config: map[string]string{"gcp:project": project}}
	}
	if err := core.SaveManifest(filepath.Join(client, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULUMI_BACKEND_URL", backend+"/unselected-fallback")
	branch := fmt.Sprintf("feature/static-%d", time.Now().UnixNano())
	infraCommand(t, client, "git", "init", "--quiet")
	buckets := map[string]string{}
	attempted := map[string]bool{}
	arguments := func(environment, operation string) []string {
		args := []string{"infra", operation, "--env", environment}
		if environment == "preview" {
			args = append(args, "--branch", branch)
		}
		return args
	}
	t.Cleanup(func() {
		for _, environment := range environments {
			if !attempted[environment] {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if buckets[environment] == "" {
				query := exec.CommandContext(ctx, binary, append(arguments(environment, "query"), "--output", "bucketName")...)
				query.Dir = client
				query.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client)
				if output, err := query.Output(); err == nil {
					buckets[environment] = strings.TrimSpace(string(output))
				}
			}

			for attempt := 0; attempt < 2; attempt++ {
				command := exec.CommandContext(ctx, binary, "ci", "flow", "on-merge", "--environment", "preview", "--branch", branch)
				command.Dir = client
				command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client)
				if environment != "preview" {
					// Persistent pm destroys require a human prompt. Only test-owned stacks
					// use Pulumi directly for unattended cleanup.
					command = exec.CommandContext(ctx, "mise", "exec", "--", "pulumi", "destroy", "--stack", environment, "--yes", "--non-interactive")
					command.Dir = filepath.Join(client, "infra")
					selected := manifest.Workspace.Infra.Environments[environment].Backend
					command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client, "PULUMI_BACKEND_URL="+manifest.Workspace.Infra.Backends[selected].URL)
				}
				if output, err := command.CombinedOutput(); err != nil {
					t.Errorf("cleanup %s: %v\n%s", environment, err, output)
					break
				}
			}
			if bucket := buckets[environment]; bucket != "" {
				output, err := exec.CommandContext(ctx, "gcloud", "storage", "buckets", "describe", "gs://"+bucket).CombinedOutput()
				if err == nil || !strings.Contains(string(output), "404") {
					t.Errorf("bucket %s deletion not verified: %v\n%s", bucket, err, output)
				}
			}
			cancel()
		}
	})

	// Act.
	t.Setenv("PM_RELEASE", "A")

	plan := infraCommand(t, client, binary, "ci", "plan", "--ci-output")
	if !strings.Contains(plan, "has_migrations=true\n") || !strings.Contains(plan, "migration_matrix=") {
		t.Fatal("CI provider did not emit migration scheduling outputs")
	}
	infraCommand(t, client, binary, "ci", "flow", "on-commit")
	for _, environment := range environments {
		t.Logf("Run environment CI flow for %s", environment)
		attempted[environment] = true
		t.Setenv("PM_BRANCH", "")
		if environment == "preview" {
			t.Setenv("PM_BRANCH", branch)
		}
		flow := "on-deploy"
		if environment == "preview" {
			flow = "on-commit"
		}
		infraCommand(t, client, binary, "ci", "flow", flow, "--environment", environment)
		bucket := strings.TrimSpace(infraCommand(t, client, binary, append(arguments(environment, "query"), "--output", "bucketName")...))
		buckets[environment] = bucket
		pull := []string{"secrets", "pull", "--project", "web", "--env", environment}
		if environment == "preview" {
			pull = append(pull, "--branch", branch)
		}
		output := infraCommand(t, client, binary, pull...)
		data, err := os.ReadFile(filepath.Join(client, "apps/web/.env.local"))
		if err != nil || !strings.Contains(string(data), "BUCKET_NAME=\""+bucket+"\"") || !strings.Contains(string(data), "APP_MESSAGE=\""+environment+"-example\"") {
			t.Fatalf("env file did not combine JSON secret and selected stack: %v", err)
		}
		if strings.Contains(output, environment+"-example") {
			t.Fatal("secrets pull printed a secret")
		}

		selected := manifest.Workspace.Infra.Environments[environment].Backend
		scope := core.InfraOptions{Environment: environment}
		if environment == "preview" {
			scope.Branch = branch
		}
		stack, err := core.ResolveStackName(scope)
		if err != nil {
			t.Fatal(err)
		}
		state := manifest.Workspace.Infra.Backends[selected].URL + "/.pulumi/stacks/premise-gcs-fixture/" + stack + ".json"
		infraCommand(t, client, "gcloud", "storage", "objects", "describe", state, "--format=value(name)")

		if page := infraCommand(t, client, "gcloud", "storage", "cat", "gs://"+bucket+"/index.html"); !strings.Contains(page, "Release A") {
			t.Fatal("static app A was not uploaded")
		}
		if migration := strings.TrimSpace(infraCommand(t, client, "gcloud", "storage", "cat", "gs://"+bucket+"/schema-version.txt")); migration != environment+":A" {
			t.Fatalf("fake migration did not receive environment: %s", migration)
		}
	}

	fresh := filepath.Join(t.TempDir(), "client")
	copyInfraFixture(t, client, fresh)
	t.Setenv("PM_BRANCH", "")
	infraCommand(t, fresh, binary, "ci", "flow", "on-deploy", "--environment", "stage")
	if bucket := strings.TrimSpace(infraCommand(t, fresh, binary, "infra", "query", "--env", "stage", "--output", "bucketName")); bucket != buckets["stage"] {
		t.Fatal("fresh checkout did not reuse the selected remote backend")
	}
	t.Setenv("PM_BRANCH", branch)
	t.Setenv("PM_RELEASE", "B")

	t.Setenv("PREMISE_TEST_FAIL_HEALTH", "preview")
	command := exec.Command(binary, "ci", "flow", "on-deploy", "--environment", "preview")
	command.Dir = client
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client)
	output, failed := command.CombinedOutput()

	// Assert.
	if failed == nil || !strings.Contains(string(output), "health") || !strings.Contains(string(output), "<h1>Release B</h1>") {
		t.Fatalf("failed B health must fail the CI flow: %v\n%s", failed, output)
	}
	bucket := buckets["preview"]
	if page := infraCommand(t, client, "gcloud", "storage", "cat", "gs://"+bucket+"/index.html"); !strings.Contains(page, "Release A") {
		t.Fatal("rollback did not restore retained A")
	}
	if migration := strings.TrimSpace(infraCommand(t, client, "gcloud", "storage", "cat", "gs://"+bucket+"/schema-version.txt")); migration != "preview:B" {
		t.Fatal("rollback reverted the fake migration")
	}
	t.Log("Failed B health restored retained A and preserved B migration marker")
	seen := map[string]bool{}
	for environment, bucket := range buckets {
		if bucket == "" || seen[bucket] {
			t.Fatalf("%s did not get an isolated bucket", environment)
		}
		seen[bucket] = true
	}
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
