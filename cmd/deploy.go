package cmd

import (
	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

func DeployCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy application artifacts and recover on failure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			release, _ := cmd.Flags().GetString("release")
			artifacts, _ := cmd.Flags().GetStringToString("artifact")
			runID, _ := cmd.Flags().GetString("run-id")
			identities, err := core.ReadReleaseEnvironment(core.ReleaseEnvironment{Release: release, Artifacts: artifacts})
			if err != nil {
				return err
			}

			environment, _ := cmd.Flags().GetString("env")
			branch, _ := cmd.Flags().GetString("branch")
			shared, _ := cmd.Flags().GetBool("shared")
			backend, _ := cmd.Flags().GetString("backend")
			yes, _ := cmd.Flags().GetBool("yes")
			options, err := core.ResolveInfraOptions(cmd.Context(), root, core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, Yes: yes})
			if err != nil {
				return err
			}
			deployment := core.DeployOptions{
				InfraOptions: options,
				RunID:        runID,
				Release:      identities.Release,
				Artifacts:    identities.Artifacts,
			}
			return core.RunApplicationDeployment(cmd.Context(), root, deployment, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	command.Flags().String("release", "", "Immutable release revision")
	command.Flags().StringToString("artifact", nil, "Project=pinned artifact identity")
	command.Flags().String("run-id", "", "Deployment identity; generated automatically for a local run")

	return command
}
