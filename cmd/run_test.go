package cmd

import (
	"bytes"
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

// Setup -----------------------------------------------------------------------

func setupRunCommandTest(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	manifest := core.Config{
		Workspace: core.Workspace{
			Name:          "fixture",
			Kind:          core.ProjectKindMonorepo,
			SchemaVersion: core.SchemaVersion,
			Projects: []core.Project{{
				Name:     "api",
				Template: "example:app",
				Path:     "apps/api",
			}},
		},
	}
	require.NoError(t, core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "apps", "api"), 0o755))

	setupFakeMise(t)
	t.Chdir(root)
}

// Tests -----------------------------------------------------------------------

func TestRunCommandForwardsStdin(t *testing.T) {
	setupRunCommandTest(t)

	var output bytes.Buffer
	runCommandWithInput(t, []string{"dev"}, "root-command-sentinel\n", &output)
	runCommandWithInput(t, []string{"api:run"}, "project-command-sentinel\n", &output)

	assert.Equal(t, "root-command-sentinel\nproject-command-sentinel\n", output.String())
}

// Helpers ---------------------------------------------------------------------

func runCommandWithInput(t *testing.T, arguments []string, input string, output io.Writer) {
	t.Helper()
	command := &cobra.Command{}
	command.SetContext(t.Context())
	command.SetIn(strings.NewReader(input))
	command.SetOut(output)
	command.SetErr(io.Discard)
	require.NoError(t, runCmd.RunE(command, arguments))
}

func setupFakeMise(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	shim := "#!/bin/sh\ncat\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
