package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestInstallRunsDevToolAndRootTaskInstallation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "premise.yaml"), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tasks.install]\nrun = 'echo installed'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "apps", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps", "api", "mise.toml"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	capture := installCommandMiseShim(t)

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetContext(t.Context())
	command.SetOut(&output)
	command.SetErr(&output)
	if err := InstallCommand().RunE(command, nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvedRoot + "|install\n" + resolvedRoot + "|install --monorepo\n" + resolvedRoot + "|run install\n" + resolvedRoot + "|run //...:install\n"
	if got := string(data); got != want {
		t.Fatalf("pm install Mise calls = %q, want %q", got, want)
	}
}

func TestInstallRunsRootTaskInstallationWhenNoProjects(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "premise.yaml"), []byte("workspace: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tasks.install]\nrun = 'echo installed'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"apps", "libs"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	capture := installCommandMiseShim(t)

	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDirectory) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetContext(t.Context())
	command.SetOut(&output)
	command.SetErr(&output)
	if err := InstallCommand().RunE(command, nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvedRoot + "|install\n" + resolvedRoot + "|run install\n"
	if got := string(data); got != want {
		t.Fatalf("pm install Mise calls = %q, want %q", got, want)
	}
	if !strings.Contains(output.String(), "No project mise.toml files found") {
		t.Fatalf("pm install output = %q, want no-project message", output.String())
	}
}

func TestInstallRunsProjectsWithoutRootInstallTask(t *testing.T) {
	t.Run("when the workspace declares only project install tasks", func(t *testing.T) {
		t.Run("it installs project dependencies without requiring a root task", func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "premise.yaml"), []byte("workspace: {}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("monorepo_root = true\n"), 0644); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(root, "libs/helper")
			if err := os.MkdirAll(project, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "mise.toml"), []byte("[tasks.install]\nrun = 'echo project'\n"), 0644); err != nil {
				t.Fatal(err)
			}
			capture := installCommandMiseShim(t)
			previous, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(previous) })
			if err := os.Chdir(root); err != nil {
				t.Fatal(err)
			}
			command := &cobra.Command{}
			command.SetContext(t.Context())
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)

			err = InstallCommand().RunE(command, nil)

			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "|run install\n") || !strings.Contains(string(data), "|run //...:install\n") {
				t.Fatalf("unexpected install calls: %s", data)
			}
		})
	})
}

func installCommandMiseShim(t *testing.T) string {
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
