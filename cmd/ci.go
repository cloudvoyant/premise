package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var (
	ciEnvironment      string
	ciReleaseMode      string
	ciProject          string
	ciChannel          string
	ciVersion          string
	ciOutputDir        string
	ciPlatform         string
	ciRootOnly         bool
	ciSkipRoot         bool
	ciPlanFlow         string
	ciPlanJSON         bool
	ciGitHubOutput     bool
	projectType        string
	projectFlow        string
	projectMode        string
	projectEligibility string
	projectJSON        bool
)

var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "Run convention-driven CI flows",
	Args:  cobra.NoArgs,
}

var ciPlanCmd = &cobra.Command{
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
		flow, err := core.ParseCIFlow(ciPlanFlow)
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
		if ciPlanJSON || ciGitHubOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(plan)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "mode: %s\\n", plan.Mode)
		return nil
	},
}

var projectsCmd = &cobra.Command{Use: "projects", Args: cobra.NoArgs}
var projectsListCmd = &cobra.Command{
	Use: "ls", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := premiseRoot()
		if err != nil {
			return err
		}
		manifest, err := core.LoadManifest(root + "/" + core.ManifestFilename)
		if err != nil {
			return err
		}
		flow := core.CIFlow(projectFlow)
		if flow == "" {
			flow = core.CIFlowOnCommit
		}
		projects, err := core.SelectCIProjects(root, manifest, flow)
		if err != nil {
			return err
		}
		if projectType != "" || projectMode != "" || projectEligibility != "" {
			filtered := projects[:0]
			for _, p := range projects {
				if projectType != "" && p.Kind != projectType {
					continue
				}
				if projectMode == "single" && len(p.Platforms()) != 1 {
					continue
				}
				if projectMode == "multi" && len(p.Platforms()) <= 1 {
					continue
				}
				if projectEligibility == "eligible" && !p.Eligible {
					continue
				}
				if projectEligibility == "excluded" && p.Eligible {
					continue
				}
				filtered = append(filtered, p)
			}
			projects = filtered
		}
		if projectJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(projects)
		}
		for _, p := range projects {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\\t%s\\t%s\\t%s\\t%s\\n", p.Name, p.Path, p.Kind, p.Source, strings.Join(p.Platforms(), ","))
		}
		return nil
	},
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
		if err := registerPackageManagerBackends(); err != nil {
			return err
		}
		if ciRootOnly && ciSkipRoot {
			return fmt.Errorf("--root-only and --skip-root cannot be used together")
		}
		options := core.CIFlowOptions{Project: ciProject, Platform: ciPlatform, OutputDir: ciOutputDir, Channel: ciChannel, Version: ciVersion, RootOnly: ciRootOnly, SkipRoot: ciSkipRoot}
		if err := core.RunCIFlowWithOptions(cmd.Context(), root, flow, ciEnvironment, releaseMode, options, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
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
	ciFlowCmd.Flags().StringVar(&ciPlatform, "platform", "", "declared target platform")
	ciFlowCmd.Flags().BoolVar(&ciRootOnly, "root-only", false, "run only the root hook (hosted orchestration)")
	ciFlowCmd.Flags().BoolVar(&ciSkipRoot, "skip-root", false, "skip the root hook")
	ciFlowCmd.Flags().StringVar(&ciReleaseMode, "release", "none", "release mode: none (publish with pm release after all flows pass)")
	ciPlanCmd.Flags().StringVar(&ciPlanFlow, "flow", "on-commit", "CI flow")
	ciPlanCmd.Flags().BoolVar(&ciPlanJSON, "json", false, "emit JSON")
	ciPlanCmd.Flags().BoolVar(&ciGitHubOutput, "github-output", false, "emit GitHub schedule JSON")
	ciCmd.AddCommand(ciFlowCmd, ciPlanCmd)
	projectsListCmd.Flags().StringVar(&projectType, "type", "", "filter by app or lib")
	projectsListCmd.Flags().StringVar(&projectFlow, "flow", "on-commit", "CI flow")
	projectsListCmd.Flags().StringVar(&projectMode, "platform-mode", "", "filter by single or multi platform")
	projectsListCmd.Flags().StringVar(&projectEligibility, "eligibility", "", "filter by eligible, excluded, or all")
	projectsListCmd.Flags().BoolVar(&projectJSON, "json", false, "emit JSON")
	projectsCmd.AddCommand(projectsListCmd)
}
