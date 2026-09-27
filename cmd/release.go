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

func init() {
	releaseCmd.Flags().BoolVar(&releaseDryRun, "dry-run", false, "plan the release without changing anything")
	releaseCmd.Flags().BoolVar(&releaseBuildOnly, "build", false, "build release artifacts without publishing")
	rootCmd.AddCommand(releaseCmd)
}
