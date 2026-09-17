package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	core "github.com/cloudvoyant/premise/core"
	misecmd "github.com/cloudvoyant/premise/internal/mise"
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
		mise := misecmd.Runner{
			Stdout:  cmd.OutOrStdout(),
			Stderr:  cmd.ErrOrStderr(),
			Ceiling: filepath.Dir(workspaceRoot),
		}
		runMise := func(args ...string) error {
			if err := mise.Run(cmd.Context(), workspaceRoot, nil, args...); err != nil {
				return fmt.Errorf("mise %s: %w", args[0], err)
			}
			return nil
		}
		if err := runMise("install"); err != nil {
			return err
		}
		hasProjectConfigs, err := core.HasProjectMiseConfigs(workspaceRoot)
		if err != nil {
			return err
		}
		if !hasProjectConfigs {
			fmt.Fprintln(cmd.OutOrStdout(), "No project mise.toml files found; installed the active workspace and global mise tools.")
			return nil
		}
		return runMise("install", "--monorepo")
	},
}
