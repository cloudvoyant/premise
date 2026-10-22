package cmd

import (
	"github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

func SecretsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "secrets",
		Short: "Run with JSON secrets or generate application env files",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(SecretsPullCommand(), SecretsExecCommand())
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
				branch, err = core.CurrentFeatureBranch(cmd.Context(), root)
			}
			if err != nil {
				return err
			}
			return core.PullProjectSecrets(cmd.Context(), root, project, environment, branch)
		},
	}
	command.Flags().String("env", "dev", "Application environment: dev, stage, prod, preview")
	command.Flags().String("project", "", "Application project (required for exec)")
	command.Flags().String("branch", "", "Feature branch identity for preview")
	return command
}

func SecretsExecCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "exec -- command [args...]",
		Short: "Run a command with environment-scoped application values",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			project, _ := cmd.Flags().GetString("project")
			environment, _ := cmd.Flags().GetString("env")
			branch, _ := cmd.Flags().GetString("branch")
			if environment == "preview" && branch == "" {
				branch, err = core.CurrentFeatureBranch(cmd.Context(), root)
			}
			if err != nil {
				return err
			}
			return core.ExecProjectSecrets(cmd.Context(), root, project, environment, branch, args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "dev", "Application environment: dev, stage, prod, preview")
	command.Flags().String("project", "", "Application project (required for exec)")
	command.Flags().String("branch", "", "Feature branch identity for preview")
	return command
}
