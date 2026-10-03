package cmd

import (
	"fmt"
	"os"
	"strings"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var releaseDryRun bool
var releaseBuildOnly bool
var releaseChannel string
var releaseExpectedVersion string
var releaseFilesDir string

var releaseCmd = &cobra.Command{
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
		if releaseDryRun && releaseBuildOnly {
			return fmt.Errorf("--dry-run and --build cannot be used together")
		}
		if err := registerPackageManagerBackends(); err != nil {
			return err
		}
		if releaseDryRun {
			var plan core.ReleasePlan
			plan, err = core.PlanStableRelease(cmd.Context(), root)
			if err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "version=%s\nshould_publish=%t\n", strings.TrimPrefix(plan.Version, "v"), !plan.Skip)
			}
			return err
		}
		if releaseBuildOnly {
			return core.BuildReleaseArtifacts(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
		}
		_, err = core.PublishStableRelease(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
		return err
	},
}

var releasePublishCmd = &cobra.Command{
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
		_, err = core.PublishRelease(cmd.Context(), root, core.ReleasePublishOptions{Channel: releaseChannel, ExpectedVersion: releaseExpectedVersion, FilesDir: releaseFilesDir}, cmd.OutOrStdout(), cmd.ErrOrStderr())
		return err
	},
}

var releasePackagesCmd = &cobra.Command{
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
		plan, err := core.PublishLanguagePackages(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if releaseExpectedVersion != "" && strings.TrimPrefix(plan.Version, "v") != strings.TrimPrefix(releaseExpectedVersion, "v") {
			return fmt.Errorf("published package version %s does not match expected version %s", plan.Version, releaseExpectedVersion)
		}
		return nil
	},
}

func currentDirectory() string { cwd, _ := os.Getwd(); return cwd }

func init() {
	releaseCmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "plan the release without changing anything")
	releaseCmd.Flags().BoolVar(&releaseBuildOnly, "build", false, "build release artifacts without publishing")
	releasePublishCmd.Flags().StringVar(&releaseChannel, "channel", "stable", "release channel (stable or rc)")
	releasePublishCmd.Flags().StringVar(&releaseExpectedVersion, "expected-version", "", "expected release version")
	releasePublishCmd.Flags().StringVar(&releaseFilesDir, "files-dir", "", "staged release files directory")
	releasePackagesCmd.Flags().StringVar(&releaseChannel, "channel", "stable", "release channel (stable or rc)")
	releasePackagesCmd.Flags().StringVar(&releaseExpectedVersion, "expected-version", "", "expected release version")
	releaseCmd.AddCommand(releasePublishCmd, releasePackagesCmd)
	rootCmd.AddCommand(releaseCmd)
}
