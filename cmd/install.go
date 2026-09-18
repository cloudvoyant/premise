package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:     "install",
	Aliases: []string{"i"},
	Short:   "Install workspace tools",
	Long:    "Installs mise tools declared by the workspace and its generated projects.",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		workspaceRoot, _, err := core.FindManifest(cwd)
		if errors.Is(err, core.ErrNotPremiseProject) {
			return errors.New("Not a premise project. Run pm init to initialize a premise project.")
		}
		if err != nil {
			return err
		}
		if filepath.Clean(cwd) != workspaceRoot {
			return fmt.Errorf("pm install must run from the premise workspace root: %s", workspaceRoot)
		}
		miseConfig := filepath.Join(workspaceRoot, "mise.toml")
		info, err := os.Stat(miseConfig)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("workspace mise configuration is missing: %s", miseConfig)
		}
		if err != nil {
			return fmt.Errorf("inspect workspace mise configuration: %w", err)
		}
		if info.IsDir() {
			return fmt.Errorf("workspace mise configuration is a directory: %s", miseConfig)
		}
		if err := core.InstallDevTools(cmd.Context(), workspaceRoot, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
			return err
		}
		return core.RunRootTask(cmd.Context(), workspaceRoot, "install", cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}
