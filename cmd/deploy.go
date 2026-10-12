package cmd

import (
	"encoding/json"
	"fmt"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Private helpers ------------------------------------------------------------

func init() {
	command := &cobra.Command{Use: "deploy", Short: "Provision, migrate, deploy and recover an environment", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
	addInfraFlags(command)
	command.Flags().String("release", "", "Immutable release revision")
	command.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	run := &cobra.Command{Use: "run", Short: command.Short, Args: cobra.NoArgs, RunE: command.RunE}
	addInfraFlags(run)
	run.Flags().String("release", "", "Immutable release revision")
	run.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	command.AddCommand(run)
	plan := &cobra.Command{Use: "plan", Short: "Show ordered deployment without effects", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
	addInfraFlags(plan)
	plan.Flags().Bool("json", false, "Print resolved state and recovery destinations as JSON")
	command.AddCommand(plan)
	rootCmd.AddCommand(command)
}
