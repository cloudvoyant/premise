package cmd

import (
	"encoding/json"
	"fmt"

	core "github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/backends"
	"github.com/cloudvoyant/premise/core/extensions"
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
	var environment, branch string
	var asJSON, githubOutput, ciOutput bool
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
			if environment != "" {
				if environment == "preview" && branch == "" {
					branch, err = extensions.CurrentFeatureBranch(cmd.Context(), root)
					if err != nil {
						return err
					}
				}
				if _, err := core.ResolveStackName(core.InfraOptions{Environment: environment, Branch: branch}); err != nil {
					return err
				}
			}
			plan.Environment, plan.Branch = environment, branch
			plan.HasInfrastructure = manifest.Workspace.Infra != nil
			if manifest.Workspace.Deployment != nil {
				plan.Migrations, err = core.PlanMigrations(root, manifest)
				if err != nil {
					return err
				}
			}
			if githubOutput {
				return core.WriteCIPlanOutputs(cmd.OutOrStdout(), "github", plan)
			}
			if ciOutput {
				return core.WriteCIPlanOutputs(cmd.OutOrStdout(), manifest.Workspace.Providers.CI, plan)
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(plan)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "mode: %s\n", plan.Mode)
			return nil
		},
	}
	command.Flags().StringVar(&flowName, "flow", "on-commit", "CI flow")
	command.Flags().StringVar(&environment, "env", "", "Deployment environment for optional migration jobs")
	command.Flags().StringVar(&branch, "branch", "", "Preview branch identity")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	command.Flags().BoolVar(&ciOutput, "ci-output", false, "emit outputs through the configured CI provider")
	command.Flags().BoolVar(&githubOutput, "github-output", false, "emit GitHub Actions output values")
	return command
}

// CIFlowCommand creates the flow command.
func CIFlowCommand() *cobra.Command {
	var environment, branch, backend, project, channel, version, outputDir, platform, releaseModeFlag string
	var rootOnly, skipRoot bool
	command := &cobra.Command{
		Use:   "flow <on-commit|on-merge|on-deploy>",
		Short: "Run one CI lifecycle",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flow, err := core.ParseCIFlow(args[0])
			if err != nil {
				return err
			}
			if flow == core.CIFlowOnDeploy && cmd.Flags().Changed("release") {
				return fmt.Errorf("--release applies only to on-commit and on-merge")
			}
			releaseMode, err := core.ParseCIReleaseMode(releaseModeFlag)
			if err != nil {
				return err
			}
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			if err := backends.RegisterPackageManagers(); err != nil {
				return err
			}
			if rootOnly && skipRoot {
				return fmt.Errorf("--root-only and --skip-root cannot be used together")
			}
			options := core.CIFlowOptions{Backend: backend, Branch: branch, Project: project, Platform: platform, OutputDir: outputDir, Channel: channel, Version: version, RootOnly: rootOnly, SkipRoot: skipRoot}
			if err := core.RunCIFlowWithOptions(cmd.Context(), root, flow, environment, releaseMode, options, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("run CI flow %s: %w", flow, err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&environment, "environment", "", "deployment environment: dev, stage, prod, or preview")
	command.Flags().StringVar(&branch, "branch", "", "feature branch identity for a preview")
	command.Flags().StringVar(&backend, "backend", "", "Configured infrastructure backend override")
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
