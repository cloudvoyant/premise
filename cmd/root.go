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

// Execute runs the root command. version is injected by main at build time.
func Execute(version, miseTemplate string) {
	workspaceMiseTemplate = miseTemplate
	rootCmd.Version = version
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.AddCommand(initCmd, installCmd, generateCmd, templateCmd)
}
