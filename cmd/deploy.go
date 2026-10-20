package cmd

import (
	"encoding/json"
	"fmt"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// DeployCommand creates the deploy command.
func DeployCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "deploy",
		Short: "Provision, migrate, deploy and recover an environment",
		Args:  cobra.NoArgs,
		RunE:  runDeploymentCommand,
	}
	addInfraFlags(command)
	command.Flags().String("release", "", "Immutable release revision")
	command.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	command.Flags().String("run-id", "", "Deployment identity; generated automatically for a local run")
	command.AddCommand(
		DeployRunCommand(),
		DeployPlanCommand(),
		DeployStartCommand(),
		DeployProvisionCommand(),
		DeployMigrateCommand(),
		DeployApplyCommand(),
		DeployCompleteCommand(),
		DeployRecoverCommand(),
		DeployUnlockCommand(),
	)
	return command
}

// DeployRunCommand creates the run command.
func DeployRunCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "run",
		Short: "Provision, migrate, deploy and recover an environment",
		Args:  cobra.NoArgs,
		RunE:  runDeploymentCommand,
	}
	addInfraFlags(command)
	command.Flags().String("release", "", "Immutable release revision")
	command.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	command.Flags().String("run-id", "", "Deployment identity; generated automatically for a local run")
	return command
}

// DeployPlanCommand creates the plan command.
func DeployPlanCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "plan",
		Short: "Show ordered deployment without effects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
		},
	}
	addInfraFlags(command)
	command.Flags().Bool("json", false, "Print resolved state and recovery destinations as JSON")
	return command
}

// DeployStartCommand creates the phase that acquires environment ownership.
func DeployStartCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "start",
		Short: "Acquire the environment lock and remember recovery inputs",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDeploymentPhaseCommand(cmd, "start") },
	}
	addDeploymentPhaseFlags(command)
	command.Flags().String("release", "", "Immutable release revision")
	command.Flags().StringToString("artifact", nil, "Project=pinned artifact reference")
	return command
}

// DeployProvisionCommand creates the database prerequisite phase.
func DeployProvisionCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "provision",
		Short: "Initialize the stack and apply database prerequisites",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDeploymentPhaseCommand(cmd, "provision") },
	}
	addDeploymentPhaseFlags(command)
	return command
}

// DeployMigrateCommand creates the phase for one application migration group.
func DeployMigrateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Run one application's migration task or an ordered migration group",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDeploymentPhaseCommand(cmd, "migrate") },
	}
	addDeploymentPhaseFlags(command)
	command.Flags().String("group", "", "Migration group from pm deploy plan --json")
	command.MarkFlagRequired("group")
	return command
}

// DeployApplyCommand creates the full update phase after all migrations pass.
func DeployApplyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "apply",
		Short: "Apply the full stack after every migration group succeeds",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDeploymentPhaseCommand(cmd, "apply") },
	}
	addDeploymentPhaseFlags(command)
	return command
}

// DeployCompleteCommand creates the application checks and release recording phase.
func DeployCompleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "complete",
		Short: "Run extra deployment tasks, check health, and remember success",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDeploymentPhaseCommand(cmd, "complete") },
	}
	addDeploymentPhaseFlags(command)
	return command
}

// DeployRecoverCommand creates the client recovery phase.
func DeployRecoverCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "recover",
		Short: "Run client recovery with the previous successful artifact references",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDeploymentPhaseCommand(cmd, "recover") },
	}
	addDeploymentPhaseFlags(command)
	command.Flags().Bool("workers-stopped", false, "Confirm all migration workers stopped before recovering an interrupted run")
	return command
}

// DeployUnlockCommand creates the explicit repair operation for an interrupted run.
func DeployUnlockCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "unlock",
		Short: "Release an interrupted run's lock after its workers are stopped",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDeploymentPhaseCommand(cmd, "unlock") },
	}
	addDeploymentPhaseFlags(command)
	return command
}

// Private helpers ------------------------------------------------------------

func runDeploymentCommand(cmd *cobra.Command, args []string) error {
	root, err := premiseRoot()
	if err != nil {
		return err
	}
	release, _ := cmd.Flags().GetString("release")
	artifacts, _ := cmd.Flags().GetStringToString("artifact")
	runID, _ := cmd.Flags().GetString("run-id")
	options, err := resolvedInfraCommandOptions(cmd, root)
	if err != nil {
		return err
	}
	deployment := core.DeployOptions{
		InfraOptions: options,
		RunID:        runID,
		Release:      release,
		Artifacts:    artifacts,
	}
	return core.RunDeployment(cmd.Context(), root, deployment, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func addDeploymentPhaseFlags(command *cobra.Command) {
	addInfraFlags(command)
	command.Flags().String("run-id", "", "Deployment identity shared by every phase and CI job")
	command.MarkFlagRequired("run-id")
}

func runDeploymentPhaseCommand(command *cobra.Command, phase string) error {
	root, err := premiseRoot()
	if err != nil {
		return err
	}
	selection, err := resolvedInfraCommandOptions(command, root)
	if err != nil {
		return err
	}
	options := core.DeployOptions{InfraOptions: selection}
	options.RunID, _ = command.Flags().GetString("run-id")
	if command.Flags().Lookup("group") != nil {
		options.Group, _ = command.Flags().GetString("group")
	}
	if command.Flags().Lookup("release") != nil {
		options.Release, _ = command.Flags().GetString("release")
		options.Artifacts, _ = command.Flags().GetStringToString("artifact")
	}
	if command.Flags().Lookup("workers-stopped") != nil {
		options.WorkersStopped, _ = command.Flags().GetBool("workers-stopped")
	}
	return core.RunDeploymentPhase(command.Context(), root, phase, options, command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr())
}
