package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCommandForwardsStdin(t *testing.T) {
	capture := setupRunCommandTest(t)
	runCommandWithInput(t, []string{"dev"}, "root-command-sentinel\n")
	runCommandWithInput(t, []string{"api:run"}, "project-command-sentinel\n")

	data, err := os.ReadFile(capture)
	require.NoError(t, err)
	assert.Equal(t,
		"run dev|root-command-sentinel\nrun run|project-command-sentinel\n",
		string(data),
	)
}

func setupRunCommandTest(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := core.NewManifest("fixture")
	manifest.Workspace.Kind = core.ProjectKindMonorepo
	manifest.Workspace.Projects = []core.Project{{Name: "api", Template: "example:app", Path: "apps/api"}}
	require.NoError(t, core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "apps", "api"), 0o755))

	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "mise-stdin")
	shim := "#!/bin/sh\nIFS= read -r input\nprintf '%s|%s\\n' \"$*\" \"$input\" >> \"$CAPTURE\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Chdir(root)
	return capture
}

func runCommandWithInput(t *testing.T, arguments []string, input string) {
	t.Helper()
	command := &cobra.Command{}
	command.SetContext(t.Context())
	command.SetIn(strings.NewReader(input))
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	require.NoError(t, runCmd.RunE(command, arguments))
}
