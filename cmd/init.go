package cmd

import (
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a premise workspace",
	Long:  `Creates premise.yaml, mise.toml, apps/, and libs/ in the current workspace. Existing mise.toml files are preserved.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		path, err := core.InitializeWorkspace(cwd, workspaceMiseTemplate)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s\n", path)
		return nil
	},
}
