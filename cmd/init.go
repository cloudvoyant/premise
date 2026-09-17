package cmd

import (
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var initProjectKind string

var initCmd = &cobra.Command{
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
		switch initProjectKind {
		case "monorepo":
			path, err = core.InitializeWorkspace(cwd, workspaceMiseTemplate)
		case "template-registry":
			path, err = core.InitializeTemplateRegistry(cwd)
		default:
			return fmt.Errorf("invalid project kind %q: expected monorepo or template-registry", initProjectKind)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s\n", path)
		return nil
	},
}

func init() {
	initCmd.Flags().StringVar(&initProjectKind, "kind", "monorepo", "project kind: monorepo or template-registry")
}
