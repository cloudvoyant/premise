package cmd

import (
	"fmt"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var (
	ciEnvironment string
	ciReleaseMode string
	ciProject     string
	ciChannel     string
	ciVersion     string
	ciOutputDir   string
)

var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "Run convention-driven CI flows",
	Args:  cobra.NoArgs,
}

var ciFlowCmd = &cobra.Command{
	Use:   "flow <on-commit|on-merge|on-release>",
	Short: "Run one CI lifecycle",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		flow, err := core.ParseCIFlow(args[0])
		if err != nil {
			return err
		}
		releaseMode, err := core.ParseCIReleaseMode(ciReleaseMode)
		if err != nil {
			return err
		}
		root, err := premiseRoot()
		if err != nil {
			return err
		}
		if flow == core.CIFlowOnPlatform {
			if err := core.RunPlatformFlow(cmd.Context(), root, ciProject, ciChannel, ciVersion, ciOutputDir, releaseMode, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("run CI flow %s: %w", flow, err)
			}
			return nil
		}
		if err := registerPackageManagerBackends(); err != nil {
			return err
		}
		if err := core.RunCIFlow(cmd.Context(), root, flow, ciEnvironment, releaseMode, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
			return fmt.Errorf("run CI flow %s: %w", flow, err)
		}
		return nil
	},
}

func init() {
	ciFlowCmd.Flags().StringVar(&ciEnvironment, "environment", "", "release environment: stage or prod")
	ciFlowCmd.Flags().StringVar(&ciProject, "project", "", "declared project template name")
	ciFlowCmd.Flags().StringVar(&ciChannel, "channel", "", "artifact channel: stable or rc")
	ciFlowCmd.Flags().StringVar(&ciVersion, "version", "", "artifact version")
	ciFlowCmd.Flags().StringVar(&ciOutputDir, "output-dir", "", "absolute artifact output directory")
	ciFlowCmd.Flags().StringVar(&ciReleaseMode, "release", "auto", "release mode: auto, none, github, or packages")
	ciCmd.AddCommand(ciFlowCmd)
}
