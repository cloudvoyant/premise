package cmd

import (
	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
	"os"
)

// Public API -----------------------------------------------------------------

// DeployCommand creates the deploy command.
func DeployCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy application artifacts and recover on failure",
		Args:  cobra.NoArgs,
		RunE:  runDeploymentCommand,
	}
	addInfraFlags(command)
	command.Flags().String("release", "", "Immutable release revision")
	command.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	command.Flags().String("run-id", "", "Deployment identity; generated automatically for a local run")

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
	if release == "" {
		release = os.Getenv("PM_RELEASE")
	}
	if len(artifacts) == 0 {
		var err error
		artifacts, err = core.ParseArtifactReferences(os.Getenv("PM_ARTIFACTS"))
		if err != nil {
			return err
		}
	}

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
	return core.RunApplicationDeployment(cmd.Context(), root, deployment, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}
