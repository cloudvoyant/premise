package cmd

import (
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Calculate repository-derived release versions",
	Args:  cobra.NoArgs,
}

var versionCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print the current version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runVersionCalculation(cmd, core.CurrentVersion)
	},
}

var versionNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Print the next version based on git history",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runVersionCalculation(cmd, core.NextVersion)
	},
}

var versionBumpCmd = &cobra.Command{
	Use:   "bump patch|minor|major",
	Short: "Print the next patch, minor, or major version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bump, err := core.ParseVersionBump(args[0])
		if err != nil {
			return err
		}
		return runVersionCalculation(cmd, func(root string) (string, error) {
			return core.BumpedVersion(root, bump)
		})
	},
}

var rcIdentifier string

var versionRcCmd = &cobra.Command{
	Use:   "rc",
	Short: "Print the next release-candidate version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runVersionCalculation(cmd, func(root string) (string, error) {
			return core.ReleaseCandidateVersion(root, rcIdentifier)
		})
	},
}

func init() {
	versionRcCmd.Flags().StringVar(&rcIdentifier, "identifier", "", "release-candidate identifier (for example a build number)")
	_ = versionRcCmd.MarkFlagRequired("identifier")
	versionCmd.AddCommand(versionCurrentCmd, versionNextCmd, versionBumpCmd, versionRcCmd)
	rootCmd.AddCommand(versionCmd)
}

func runVersionCalculation(cmd *cobra.Command, calculate func(string) (string, error)) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	root, _, err := core.FindManifest(cwd)
	if err != nil {
		return err
	}
	version, err := calculate(root)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), version)
	return nil
}
