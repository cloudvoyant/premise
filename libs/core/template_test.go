package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTemplateContractsPreserveBuildCache(t *testing.T) {
	if _, err := exec.LookPath("mise"); err != nil {
		t.Skip("mise is not installed")
	}
	root := t.TempDir()
	project := filepath.Join(root, "templates", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	tasks, err := ContractTasks("app")
	if err != nil {
		t.Fatal(err)
	}
	var config bytes.Buffer
	for _, name := range tasks {
		command := "echo ok"
		if name == "clean" {
			command = "exit 99"
		}
		fmt.Fprintf(&config, "[tasks.%q]\nrun = %q\n", name, command)
	}
	if err := os.WriteFile(filepath.Join(project, "mise.toml"), config.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := NewManifest("fixture")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{
		templateFixture("app", "app"),
	}}
	var output bytes.Buffer
	if err := TestTemplateContracts(context.Background(), root, manifest, &output, io.Discard); err != nil {
		t.Fatalf("clean must be inspected, not run: %v\n%s", err, output.String())
	}
	if !bytes.Contains(output.Bytes(), []byte("mise task info clean")) ||
		bytes.Contains(output.Bytes(), []byte("mise run clean")) {
		t.Fatalf("unexpected clean handling: %s", output.String())
	}
}
