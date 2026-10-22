package cmd

import (
	"github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/extensions"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

func SecretsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "secrets",
		Short: "Generate local env files using project tasks",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(SecretsPullCommand())
	return command
}

func SecretsPullCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "pull",
		Short: "Generate env files using the project secrets:pull task",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			project, _ := cmd.Flags().GetString("project")
			environment, _ := cmd.Flags().GetString("env")
			branch, _ := cmd.Flags().GetString("branch")
			if environment == "preview" && branch == "" {
				branch, err = extensions.CurrentFeatureBranch(cmd.Context(), root)
			}
			if err != nil {
				return err
			}
			return core.PullProjectSecrets(cmd.Context(), root, project, environment, branch)
		},
	}
	command.Flags().String("env", "dev", "Application environment: dev, stage, prod, preview")
	command.Flags().String("project", "", "Application project; defaults to all projects with secrets:pull")
	command.Flags().String("branch", "", "Feature branch identity for preview")
	return command
}
