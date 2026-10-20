package cmd

import (
	"encoding/json"
	"fmt"
	"io"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// CICommand creates the ci command.
func CICommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "ci",
		Short: "Run convention-driven CI flows",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(CIFlowCommand(), CIPlanCommand())
	return command
}

// CIPlanCommand creates the plan command.
func CIPlanCommand() *cobra.Command {
	var flowName string
	var asJSON, githubOutput bool
	command := &cobra.Command{
		Use:  "plan",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			manifest, err := core.LoadManifest(root + "/" + core.ManifestFilename)
			if err != nil {
				return err
			}
			flow, err := core.ParseCIFlow(flowName)
			if err != nil {
				return err
			}
			projects, err := core.SelectCIProjects(root, manifest, flow)
			if err != nil {
				return err
			}
			plan, err := core.PlanCISchedule(projects)
			if err != nil {
				return err
			}
			if githubOutput {
				return writeCIGitHubOutput(cmd.OutOrStdout(), plan)
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plan)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "mode: %s\n", plan.Mode)
			return nil
		},
	}
	command.Flags().StringVar(&flowName, "flow", "on-commit", "CI flow")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	command.Flags().BoolVar(&githubOutput, "github-output", false, "emit GitHub Actions output values")
	return command
}

// CIFlowCommand creates the flow command.
func CIFlowCommand() *cobra.Command {
	var environment, project, channel, version, outputDir, platform, releaseModeFlag string
	var rootOnly, skipRoot bool
	command := &cobra.Command{
		Use:   "flow <on-commit|on-merge|on-release>",
		Short: "Run one CI lifecycle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flow, err := core.ParseCIFlow(args[0])
			if err != nil {
				return err
			}
			releaseMode, err := core.ParseCIReleaseMode(releaseModeFlag)
			if err != nil {
				return err
			}
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			if err := registerPackageManagerBackends(); err != nil {
				return err
			}
			if rootOnly && skipRoot {
				return fmt.Errorf("--root-only and --skip-root cannot be used together")
			}
			options := core.CIFlowOptions{Project: project, Platform: platform, OutputDir: outputDir, Channel: channel, Version: version, RootOnly: rootOnly, SkipRoot: skipRoot}
			if err := core.RunCIFlowWithOptions(cmd.Context(), root, flow, environment, releaseMode, options, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("run CI flow %s: %w", flow, err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&environment, "environment", "", "release environment: stage or prod")
	command.Flags().StringVar(&project, "project", "", "declared project template name")
	command.Flags().StringVar(&channel, "channel", "", "artifact channel: stable or rc")
	command.Flags().StringVar(&version, "version", "", "artifact version")
	command.Flags().StringVar(&outputDir, "output-dir", "", "absolute artifact output directory")
	command.Flags().StringVar(&platform, "platform", "", "declared target platform")
	command.Flags().BoolVar(&rootOnly, "root-only", false, "run only the root hook (hosted orchestration)")
	command.Flags().BoolVar(&skipRoot, "skip-root", false, "skip the root hook")
	command.Flags().StringVar(&releaseModeFlag, "release", "none", "release mode: none (publish with pm release after all flows pass)")
	return command
}

// Private helpers ------------------------------------------------------------

// writeCIGitHubOutput writes values directly to a GitHub Actions step output file.
func writeCIGitHubOutput(output io.Writer, plan core.CISchedule) error {
	matrix, err := json.Marshal(struct {
		Include []core.CIMatrixRow `json:"include"`
	}{plan.Matrix})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "mode=%s\nrunner=%s\nnative=%t\nmatrix=%s\n", plan.Mode, plan.Runner, plan.HasNative, matrix)
	return err
}
