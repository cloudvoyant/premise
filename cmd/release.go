package cmd

import (
	"fmt"
	"os"
	"strings"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var releaseCmd = &cobra.Command{
	Use:   "release [plan|prepare|github|packages|snapshot]",
	Short: "Build and publish convention-driven releases",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		root, _, err := core.FindManifest(cwd)
		if err != nil {
			return err
		}
		mode := ""
		if len(args) == 1 {
			mode = args[0]
		}
		if err := registerPackageManagerPlugins(); err != nil {
			return err
		}
		switch mode {
		case "":
			_, err = core.PublishStableRelease(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
		case "plan":
			var plan core.ReleasePlan
			plan, err = core.PlanStableRelease(cmd.Context(), root)
			if err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "version=%s\nshould_publish=%t\n", strings.TrimPrefix(plan.Version, "v"), !plan.Skip)
			}
		case "prepare":
			_, err = core.PrepareStableRelease(cmd.Context(), root, cmd.OutOrStdout())
		case "github":
			_, err = core.PublishGitHubRelease(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
		case "packages":
			_, err = core.PublishLanguagePackages(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
		case "snapshot":
			err = core.BuildReleaseSnapshot(cmd.Context(), root, cmd.OutOrStdout(), cmd.ErrOrStderr())
		default:
			return fmt.Errorf("unknown release mode %q: expected plan, prepare, github, packages, or snapshot", mode)
		}
		return err
	},
}

func init() {
	rootCmd.AddCommand(releaseCmd)
}
