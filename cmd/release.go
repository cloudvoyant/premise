package cmd

import (
	"fmt"
	"os"
	"strings"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// ReleaseCommand creates the release command.
func ReleaseCommand() *cobra.Command {
	var dryRun, buildOnly bool
	command := &cobra.Command{
		Use:   "release",
		Short: "Build and publish a convention-driven release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current directory: %w", err)
			}
			root, _, err := core.FindManifest(cwd)
			if err != nil {
				return err
			}
			if dryRun && buildOnly {
				return fmt.Errorf("--dry-run and --build cannot be used together")
			}
			if err := registerPackageManagerBackends(); err != nil {
				return err
			}
			if dryRun {
				var plan core.ReleasePlan
				plan, err = core.PlanStableRelease(cmd.Context(), root)
				if err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "version=%s\nshould_publish=%t\n", strings.TrimPrefix(plan.Version, "v"), !plan.Skip)
				}
				return err
			}
			if buildOnly {
				return core.BuildReleaseArtifacts(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			_, err = core.PublishStableRelease(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
			return err
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "plan the release without changing anything")
	command.Flags().BoolVar(&buildOnly, "build", false, "build release artifacts without publishing")
	command.AddCommand(ReleasePublishCommand(), ReleasePackagesCommand())
	return command
}

// ReleasePublishCommand creates the publish command.
func ReleasePublishCommand() *cobra.Command {
	var channel, expectedVersion, filesDir string
	command := &cobra.Command{
		Use:   "publish",
		Short: "publish one combined GitHub release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, _, err := core.FindManifest(currentDirectory())
			if err != nil {
				return err
			}
			if err := registerPackageManagerBackends(); err != nil {
				return err
			}
			groups, err := core.ExpectedReleaseGroups(root)
			if err != nil {
				return err
			}
			_, err = core.PublishRelease(cmd.Context(), root, core.ReleasePublishOptions{Channel: channel, ExpectedVersion: expectedVersion, FilesDir: filesDir, ExpectedGroups: groups}, cmd.OutOrStdout(), cmd.ErrOrStderr())
			return err
		},
	}
	command.Flags().StringVar(&channel, "channel", "stable", "release channel (stable or rc)")
	command.Flags().StringVar(&expectedVersion, "expected-version", "", "expected release version")
	command.Flags().StringVar(&filesDir, "files-dir", "", "staged release files directory")
	return command
}

// ReleasePackagesCommand creates the packages command.
func ReleasePackagesCommand() *cobra.Command {
	var channel, expectedVersion string
	command := &cobra.Command{
		Use:   "packages",
		Short: "publish registry packages after release verification",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, _, err := core.FindManifest(currentDirectory())
			if err != nil {
				return err
			}
			if err := registerPackageManagerBackends(); err != nil {
				return err
			}
			_, err = core.PublishReleasePackages(cmd.Context(), root, channel, expectedVersion, cmd.OutOrStdout(), cmd.ErrOrStderr())
			return err
		},
	}
	command.Flags().StringVar(&channel, "channel", "stable", "release channel (stable or rc)")
	command.Flags().StringVar(&expectedVersion, "expected-version", "", "expected release version")
	return command
}

// Private helpers ------------------------------------------------------------

func currentDirectory() string { cwd, _ := os.Getwd(); return cwd }
