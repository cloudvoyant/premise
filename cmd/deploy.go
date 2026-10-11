package cmd

import (
	"errors"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Private helpers ------------------------------------------------------------

var deployCmd = &cobra.Command{Use: "deploy", Short: "Apply infrastructure and its configured application artifacts"}

func init() {
	run := &cobra.Command{
		Use: "run [project]", Aliases: []string{"on-deploy"}, Args: cobra.MaximumNArgs(1),
		Short: "Apply Pulumi infrastructure or run a project deployment task",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			options := infraCommandOptions(cmd)
			task, _ := cmd.Flags().GetString("task")
			if task != "" {
				if len(args) != 1 || options.All || cmd.Flags().Changed("backend") || options.Yes {
					return errors.New("--task requires a project and cannot be combined with --all, --backend, or --yes")
				}
				return core.RunDeploymentTask(cmd.Context(), root, args[0], task, options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			if len(args) == 1 {
				options.Project = args[0]
			} else {
				options.All = true
			}
			return core.RunInfra(cmd.Context(), root, "apply", options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	addInfraFlags(run)
	run.Flags().String("task", "", "Run a project-defined deployment task instead of applying Pulumi")
	run.Flags().Bool("all", false, "Apply the whole workspace's infrastructure projects")
	run.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	deployCmd.AddCommand(run)
	rootCmd.AddCommand(deployCmd)
}
