package core

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRootTaskPassesArguments(t *testing.T) {
	root := t.TempDir()
	capture := installMiseTaskShim(t)

	if err := RunRootTask(t.Context(), root, "lint:fix", nil, nil, "--check"); err != nil {
		t.Fatal(err)
	}
	want := canonicalTaskPath(t, root) + "|run lint:fix --check\n"
	if got := readTaskCapture(t, capture); got != want {
		t.Fatalf("RunRootTask() = %q, want %q", got, want)
	}
}

func TestRunProjectTaskRunsFromDeclaredProject(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("fixture")
	manifest.Workspace.Kind = ProjectKindMonorepo
	manifest.Workspace.Projects = []Project{{Name: "api", Template: "example:app", Path: "apps/api"}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(root, "apps", "api")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	capture := installMiseTaskShim(t)

	if err := RunProjectTask(t.Context(), root, "api", "test", nil, nil, "--all"); err != nil {
		t.Fatal(err)
	}
	want := canonicalTaskPath(t, projectRoot) + "|run test --all\n"
	if got := readTaskCapture(t, capture); got != want {
		t.Fatalf("RunProjectTask() = %q, want %q", got, want)
	}
}

func TestRunProjectTaskRejectsUnknownProject(t *testing.T) {
	root := t.TempDir()
	if err := SaveManifest(filepath.Join(root, ManifestFilename), NewManifest("fixture")); err != nil {
		t.Fatal(err)
	}

	err := RunProjectTask(t.Context(), root, "missing", "test", nil, nil)
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("RunProjectTask() error = %v, want ErrProjectNotFound", err)
	}
}

func TestInstallDevToolsInstallsWorkspaceAndProjects(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "apps", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps", "api", "mise.toml"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	capture := installMiseTaskShim(t)
	var output bytes.Buffer

	if err := InstallDevTools(t.Context(), root, &output, &output); err != nil {
		t.Fatal(err)
	}
	canonicalRoot := canonicalTaskPath(t, root)
	want := canonicalRoot + "|install\n" + canonicalRoot + "|install --monorepo\n"
	if got := readTaskCapture(t, capture); got != want {
		t.Fatalf("InstallDevTools() = %q, want %q", got, want)
	}
}

func installMiseTaskShim(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "mise-calls")
	shim := "#!/bin/sh\nprintf '%s|%s\\n' \"$PWD\" \"$*\" >> \"$CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	return capture
}

func readTaskCapture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\\", "/")
}

func canonicalTaskPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
