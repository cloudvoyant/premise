package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPulumiLocalLifecycle verifies real CLI state persistence without cloud resources.
// Public API -----------------------------------------------------------------

func TestPulumiLocalLifecycle(t *testing.T) {
	binary := os.Getenv("PREMISE_TEST_PULUMI_BIN")
	if binary == "" {
		t.Skip("set PREMISE_TEST_PULUMI_BIN to run the real Pulumi local-state test")
	}
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PULUMI_HOME", t.TempDir())
	t.Setenv("PULUMI_CONFIG_PASSPHRASE", "test-only-passphrase")
	t.Setenv("PULUMI_SKIP_UPDATE_CHECK", "true")
	root := t.TempDir()
	program := filepath.Join(root, "infra/environment")
	if err := os.MkdirAll(program, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(program, "Pulumi.yaml"), []byte("name: premise-local-test\nruntime: yaml\noutputs:\n  marker: persistent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := infraTestManifest()
	manifest.Workspace.Infra.Backends["remote"] = InfraBackend{URL: "file://" + t.TempDir(), AllowLocal: true}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	options := InfraOptions{Environment: "preview", Branch: "feature/state", Yes: true}
	manifest.Workspace.Infra.Environments["preview"] = InfraEnvironment{}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	ctx := context.Background()
	for _, operation := range []string{"init", "preview", "apply", "apply"} {
		if err := RunInfra(ctx, root, operation, options, nil, &output, &diagnostics); err != nil {
			t.Fatalf("%s: %v\n%s\n%s", operation, err, output.String(), diagnostics.String())
		}
	}
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := resolvePrerequisiteTargets(ctx, infraCommandRunner{stdout: &output, stderr: &diagnostics}, destination, []string{"*pulumi:pulumi:Stack*"}, nil)
	if err != nil {
		t.Fatalf("saved plan validation: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("empty saved plan targets")
	}
	output.Reset()
	if err := RunInfra(ctx, root, "output", options, nil, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(output.Bytes(), &values); err != nil {
		t.Fatalf("%v: %s", err, output.String())
	}
	if values["marker"] != "persistent" {
		t.Fatal(values)
	}
	if err := RunInfra(ctx, root, "destroy", options, nil, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
}
