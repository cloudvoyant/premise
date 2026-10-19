// Package cmd defines the premise CLI command tree.
package cmd

import (
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

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
	rootCmd.AddCommand(
		NewCICommand(),
		NewDeployCommand(),
		NewGenerateCommand(),
		NewInfraCommand(),
		NewInitCommand(),
		NewInstallCommand(),
		NewProjectsCommand(),
		NewReleaseCommand(),
		NewRunCommand(),
		NewSecretsCommand(),
		NewTemplateCommand(),
		NewUpdateCommand(),
		NewVersionCommand(),
	)
}

// Private helpers ------------------------------------------------------------

var workspaceScaffold core.WorkspaceScaffold
