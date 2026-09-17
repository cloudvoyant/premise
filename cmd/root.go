// Package cmd defines the premise CLI command tree.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var workspaceMiseTemplate string

var rootCmd = &cobra.Command{
	Use:   "pm",
	Short: "Convention-driven project lifecycle toolkit built on Mise",
	Long: `premise scaffolds monorepo projects from template registries, keeps
projects converged on their templates, and runs lifecycle tasks in CI.`,
	SilenceUsage: true,
}

// Execute runs the root command.
func Execute(miseTemplate string) {
	workspaceMiseTemplate = miseTemplate
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(ciCmd, initCmd, installCmd, generateCmd, templateCmd)
}
