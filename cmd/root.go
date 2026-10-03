// Package cmd defines the premise CLI command tree.
package cmd

import (
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var workspaceScaffold core.WorkspaceScaffold

var rootCmd = &cobra.Command{
	Use:   "pm",
	Short: "Convention-driven project lifecycle toolkit built on Mise",
	Long: `premise scaffolds monorepo projects from template registries, keeps
projects converged on their templates, and runs lifecycle tasks in CI.`,
	SilenceUsage: true,
}

// Execute runs the root command.
func Execute(miseTemplate string, assets ...core.WorkflowAsset) {
	workspaceScaffold = core.WorkspaceScaffold{MiseTemplate: miseTemplate, WorkflowAssets: assets}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(ciCmd, projectsCmd, initCmd, installCmd, updateCmd, generateCmd, runCmd, templateCmd)
}
