package cmd

import (
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// NewInitCommand creates the init command.
func NewInitCommand() *cobra.Command {
	var projectKind string
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize a Premise project",
		Long:  `Creates either a monorepo workspace or a template registry. Existing files are preserved.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current directory: %w", err)
			}
			var path string
			switch projectKind {
			case "monorepo":
				path, err = core.InitializeWorkspace(cwd, workspaceScaffold.MiseTemplate, workspaceScaffold.WorkflowAssets...)
			case "template-registry":
				path, err = core.InitializeTemplateRegistry(cwd)
			default:
				return fmt.Errorf("invalid project kind %q: expected monorepo or template-registry", projectKind)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s\n", path)
			return nil
		},
	}
	command.Flags().StringVar(&projectKind, "kind", "monorepo", "project kind: monorepo or template-registry")
	return command
}
