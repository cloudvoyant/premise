// Package cmd defines the premise CLI command tree.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var workspaceMiseTemplate string

var rootCmd = &cobra.Command{
	Use:   "pm",
	Short: "Opinionated monorepo lifecycle toolkit built on mise",
	Long: `premise scaffolds monorepo projects from templates, keeps descendant
projects converged on those templates, and wires mise tasks into CI.`,
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
	rootCmd.AddCommand(initCmd, installCmd, generateCmd, templateCmd)
}
