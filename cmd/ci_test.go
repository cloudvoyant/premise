package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

func TestCIPlanInstancesKeepTheirOwnFlagValues(t *testing.T) {
	root := t.TempDir()
	manifest := core.NewManifest("fixture")
	manifest.Workspace.Projects = []core.Project{{Name: "web", Template: "registry:app", Path: "apps/web"}}
	if err := os.MkdirAll(filepath.Join(root, "apps/web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	first := CIPlanCommand()
	second := CIPlanCommand()
	if err := first.Flags().Set("flow", "not-a-flow"); err != nil {
		t.Fatal(err)
	}
	if err := first.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	first.SetOut(io.Discard)
	first.SetErr(io.Discard)
	first.SetArgs([]string{})
	if err := first.Execute(); err == nil {
		t.Fatal("invalid flow was accepted")
	}
	var output bytes.Buffer
	second.SetOut(&output)
	second.SetErr(io.Discard)
	second.SetArgs([]string{})
	if err := second.Execute(); err != nil {
		t.Fatalf("another instance changed this command: %v", err)
	}
	if !strings.HasPrefix(output.String(), "mode: ") {
		t.Fatalf("another instance changed the output format: %s", output.String())
	}
}
