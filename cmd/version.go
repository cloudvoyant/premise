package cmd

import (
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// VersionCommand creates the version command.
func VersionCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "version",
		Short: "Calculate repository-derived release versions",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(VersionCurrentCommand(), VersionNextCommand(), VersionBumpCommand(), VersionRCCommand())
	return command
}

// VersionCurrentCommand creates the current command.
func VersionCurrentCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "current",
		Short: "Print the current version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVersionCalculation(cmd, core.CurrentVersion)
		},
	}

	return command
}

// VersionNextCommand creates the next command.
func VersionNextCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "next",
		Short: "Print the next version based on git history",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVersionCalculation(cmd, core.NextVersion)
		},
	}

	return command
}

// VersionBumpCommand creates the bump command.
func VersionBumpCommand() *cobra.Command {
	command := &cobra.Command{
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

	return command
}

// VersionRCCommand creates the rc command.
func VersionRCCommand() *cobra.Command {
	var identifier string
	command := &cobra.Command{
		Use:   "rc",
		Short: "Print the next release-candidate version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runVersionCalculation(cmd, func(root string) (string, error) {
				return core.ReleaseCandidateVersion(root, identifier)
			})
		},
	}
	command.Flags().StringVar(&identifier, "identifier", "", "release-candidate identifier (for example a build number)")
	_ = command.MarkFlagRequired("identifier")
	return command
}

// Private helpers ------------------------------------------------------------

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
