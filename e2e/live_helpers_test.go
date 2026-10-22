package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/cloudvoyant/premise/core"
)

// Private helpers ------------------------------------------------------------

type liveClient struct {
	binary   string
	root     string
	branch   string
	manifest core.Config
	touched  map[string]bool
	buckets  map[string]string
}

func requireLiveInfrastructure(t *testing.T) {
	t.Helper()
	if os.Getenv("PREMISE_TEST_GCS") != "1" {
		t.Skip("set PREMISE_TEST_GCS=1 to run against live GCS")
	}
	if os.Getenv("PREMISE_TEST_GCP_PROJECT") == "" || !strings.HasPrefix(os.Getenv("PULUMI_BACKEND_URL"), "gs://") {
		t.Fatal("select PREMISE_TEST_GCP_PROJECT and an existing gs:// PULUMI_BACKEND_URL")
	}
}

func newLiveClient(t *testing.T, binary string) *liveClient {
	t.Helper()
	requireLiveInfrastructure(t)
	root := filepath.Join(t.TempDir(), "client")
	copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/client"), root)
	return configureLiveClient(t, binary, root)
}

func configureLiveClient(t *testing.T, binary, root string) *liveClient {
	t.Helper()
	requireLiveInfrastructure(t)
	t.Setenv("PULUMI_CONFIG_PASSPHRASE", "")
	t.Setenv("PM_RELEASE", "A")
	t.Setenv("PM_BRANCH", "")
	bin := t.TempDir()
	if err := os.Symlink(binary, filepath.Join(bin, "pm")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	backend := strings.TrimRight(os.Getenv("PULUMI_BACKEND_URL"), "/") + fmt.Sprintf("/run-%d", time.Now().UnixNano())
	manifest.Workspace.Infra.Backend = "primary"
	manifest.Workspace.Infra.Backends = map[string]core.InfraBackend{
		"primary": {URL: backend}, "alternate": {URL: backend + "/alternate"},
	}
	manifest.Workspace.Infra.Environments = map[string]core.InfraEnvironment{}
	for _, environment := range []string{"dev", "stage", "prod", "preview"} {
		selected := "primary"
		if environment == "prod" || environment == "preview" {
			selected = "alternate"
		}
		manifest.Workspace.Infra.Environments[environment] = core.InfraEnvironment{
			Backend: selected, Config: map[string]string{"gcp:project": os.Getenv("PREMISE_TEST_GCP_PROJECT")},
		}
	}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULUMI_BACKEND_URL", backend+"/unselected-fallback")
	client := &liveClient{binary: binary, root: root, branch: fmt.Sprintf("feature/static-%d", time.Now().UnixNano()), manifest: manifest, touched: map[string]bool{}, buckets: map[string]string{}}
	runCommand(t, root, "git", "init", "--quiet", "--initial-branch=main")
	t.Cleanup(func() { client.cleanup(t) })
	return client
}

func (client *liveClient) scope(environment string) []string {
	args := []string{"--env", environment}
	if environment == "preview" {
		args = append(args, "--branch", client.branch)
	}
	return args
}

func (client *liveClient) infra(t *testing.T, operation, environment string, extra ...string) string {
	t.Helper()
	client.touched[environment] = true
	args := append([]string{"infra", operation}, client.scope(environment)...)
	args = append(args, extra...)
	return runCommand(t, client.root, client.binary, args...)
}

func (client *liveClient) flow(t *testing.T, flow, environment, release string) string {
	t.Helper()
	client.touched[environment] = true
	t.Setenv("PM_RELEASE", release)
	args := []string{"ci", "flow", flow, "--environment", environment}
	if environment == "preview" {
		args = append(args, "--branch", client.branch)
	}
	return runCommand(t, client.root, client.binary, args...)
}

func (client *liveClient) bucket(t *testing.T, environment string) string {
	t.Helper()
	bucket := strings.TrimSpace(client.infra(t, "query", environment, "--output", "bucketName"))
	if bucket == "" {
		t.Fatal("stack did not export bucketName")
	}
	client.buckets[environment] = bucket
	return bucket
}

func (client *liveClient) object(t *testing.T, environment, object string) string {
	t.Helper()
	return runCommand(t, client.root, "gcloud", "storage", "cat", "gs://"+client.bucket(t, environment)+"/"+object)
}

func (client *liveClient) assertBucketDeleted(t *testing.T, bucket string) {
	t.Helper()
	output := commandFailure(t, client.root, "gcloud", "storage", "buckets", "describe", "gs://"+bucket)
	if !strings.Contains(output, "404") {
		t.Fatalf("expected bucket absence, got: %s", output)
	}
}

func (client *liveClient) assertSavedBackend(t *testing.T, environment string) {
	t.Helper()
	settings := client.manifest.Workspace.Infra.Environments[environment]
	scope := core.InfraOptions{Environment: environment}
	if environment == "preview" {
		scope.Branch = client.branch
	}
	stack, err := core.ResolveStackName(scope)
	if err != nil {
		t.Fatal(err)
	}
	state := client.manifest.Workspace.Infra.Backends[settings.Backend].URL + "/.pulumi/stacks/premise-gcs-fixture/" + stack + ".json"
	runCommand(t, client.root, "gcloud", "storage", "objects", "describe", state, "--format=value(name)")
}

// cleanup is a safety net. Scenario assertions exercise preview deletion explicitly.
func (client *liveClient) cleanup(t *testing.T) {
	t.Helper()
	for _, environment := range []string{"dev", "stage", "prod", "preview"} {
		if !client.touched[environment] {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		// Capture the name before destroying, including after a partially failed apply.
		query := exec.CommandContext(ctx, client.binary, append(append([]string{"infra", "query"}, client.scope(environment)...), "--output", "bucketName")...)
		query.Dir = client.root
		query.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client.root)
		if output, err := query.Output(); err == nil {
			client.buckets[environment] = strings.TrimSpace(string(output))
		}
		args := []string{"ci", "flow", "on-merge", "--environment", "preview", "--branch", client.branch}
		command := exec.CommandContext(ctx, client.binary, args...)
		command.Dir = client.root
		command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client.root)
		if environment != "preview" {
			// Only test-owned persistent stacks bypass the CLI's human confirmation.
			command = exec.CommandContext(ctx, "mise", "exec", "--", "pulumi", "destroy", "--stack", environment, "--yes", "--non-interactive")
			command.Dir = filepath.Join(client.root, "infra")
			selected := client.manifest.Workspace.Infra.Environments[environment].Backend
			command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client.root, "PULUMI_BACKEND_URL="+client.manifest.Workspace.Infra.Backends[selected].URL)
		}
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("cleanup %s: %v\n%s", environment, err, output)
		}
		if bucket := client.buckets[environment]; bucket != "" {
			output, err := exec.CommandContext(ctx, "gcloud", "storage", "buckets", "describe", "gs://"+bucket).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "404") {
				t.Errorf("bucket %s deletion not verified: %v\n%s", bucket, err, output)
			}
		}
		cancel()
	}
}
