package cmd

import (
	"encoding/json"
	"fmt"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

var deployCmd = &cobra.Command{Use: "deploy", Short: "Provision, migrate, deploy and recover an environment", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
	root, err := premiseRoot()
	if err != nil {
		return err
	}
	release, _ := cmd.Flags().GetString("release")
	artifacts, _ := cmd.Flags().GetStringToString("artifact")
	options, err := resolvedInfraCommandOptions(cmd, root)
	if err != nil {
		return err
	}
	return core.RunDeployment(cmd.Context(), root, core.DeployOptions{InfraOptions: options, Release: release, Artifacts: artifacts}, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}}

var deployRunCmd = &cobra.Command{Use: "run", Short: deployCmd.Short, Args: cobra.NoArgs, RunE: deployCmd.RunE}

var deployPlanCmd = &cobra.Command{Use: "plan", Short: "Show ordered deployment without effects", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
	root, err := premiseRoot()
	if err != nil {
		return err
	}
	options, err := resolvedInfraCommandOptions(cmd, root)
	if err != nil {
		return err
	}
	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		plan, err := core.ResolveDeploymentPlan(root, options)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(plan)
	}
	steps, err := core.PlanDeployment(root, options)
	if err != nil {
		return err
	}
	for _, step := range steps {
		fmt.Fprintln(cmd.OutOrStdout(), step)
	}
	return nil
}}

func init() {
	addInfraFlags(deployCmd)
	deployCmd.Flags().String("release", "", "Immutable release revision")
	deployCmd.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	addInfraFlags(deployRunCmd)
	deployRunCmd.Flags().String("release", "", "Immutable release revision")
	deployRunCmd.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	addInfraFlags(deployPlanCmd)
	deployPlanCmd.Flags().Bool("json", false, "Print resolved state and recovery destinations as JSON")
	deployCmd.AddCommand(deployRunCmd, deployPlanCmd)
	rootCmd.AddCommand(deployCmd)
}
