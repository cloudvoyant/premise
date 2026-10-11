package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInfrastructureRuntimeSecretsDoNotLeakThroughLogs(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "pulumi")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$PM_APP_SECRETS\" \"$VERCEL_TOKEN\"\nprintf '%s\\n' \"$PM_APP_SECRETS\" >&2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VERCEL_TOKEN", "administration-token-test")
	var output, diagnostics bytes.Buffer
	r := infraCommandRunner{stdout: &output, stderr: &diagnostics}
	if err := r.run(context.Background(), directory, map[string]string{"PM_APP_SECRETS": `{"web":{"APP_MARKER":"runtime-secret-test"}}`}, "pulumi", "preview"); err != nil {
		t.Fatal(err)
	}
	text := output.String() + diagnostics.String()
	if strings.Contains(text, "runtime-secret-test") || strings.Contains(text, "administration-token-test") {
		t.Fatal("runtime/provider credential leaked")
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatal("no redaction evidence")
	}
}

func TestRuntimeSecretsSelectOnlyAppsBoundToOwningProgram(t *testing.T) {
	root := t.TempDir()
	manifest := infraTestManifest()
	settings := &ProjectSecrets{Config: "fnox.toml", Profiles: map[string]string{"dev": "dev"}, Keys: []string{"APP_MARKER"}}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "web", Template: "registry/app", Path: "apps/web", Secrets: settings}, Project{Name: "other", Template: "registry/app", Path: "apps/other", Secrets: settings}, Project{Name: "shared", Template: "registry/shared", Path: "infra/shared", Infra: &InfraDeclaration{Role: "program", Scope: "shared"}})
	manifest.Workspace.Infra.Bindings = map[string]InfraBinding{"web": {Program: "environment", Artifact: "."}, "other": {Program: "shared", Artifact: "."}}
	for _, directory := range []string{"apps/web", "apps/other", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, app := range []string{"web", "other"} {
		if err := os.WriteFile(filepath.Join(root, "apps", app, "fnox.toml"), []byte("[profiles.dev.secrets]\nAPP_MARKER = { provider = \"gcp\", value = \"fixture\" }\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin/mise"), []byte("#!/bin/sh\nprintf 'fixture-runtime-secret\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	data, err := infrastructureAppSecrets(context.Background(), root, InfraResolution{Program: manifest.Workspace.Projects[0]}, InfraOptions{Environment: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, `"web"`) || strings.Contains(data, `"other"`) {
		t.Fatal("runtime secrets crossed program ownership")
	}
}
