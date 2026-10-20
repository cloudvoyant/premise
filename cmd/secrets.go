package cmd

import (
	"github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// SecretsCommand creates the secrets command.
func SecretsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "secrets",
		Short: "Pull local secrets or copy application values to CI",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(SecretsPullCommand(), SecretsExecCommand(), SecretsSyncCommand())
	return command
}

// SecretsPullCommand creates the pull command.
func SecretsPullCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "pull",
		Short: "Write local application env files from fnox",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, project, environment, branch, err := secretCommandOptions(cmd)
			if err != nil {
				return err
			}
			return core.PullProjectSecrets(cmd.Context(), root, project, environment, branch)
		},
	}
	addSecretFlags(command)
	return command
}

// SecretsExecCommand creates the exec command.
func SecretsExecCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "exec -- command [args...]",
		Short: "Run a command with environment-scoped application values",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, project, environment, branch, err := secretCommandOptions(cmd)
			if err != nil {
				return err
			}
			return core.ExecProjectSecrets(cmd.Context(), root, project, environment, branch, args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	addSecretFlags(command)
	return command
}

// SecretsSyncCommand creates the sync command.
func SecretsSyncCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "sync",
		Short: "Copy local fnox application values to CI secrets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, project, environment, branch, err := secretCommandOptions(cmd)
			if err != nil {
				return err
			}
			repository, _ := cmd.Flags().GetString("repository")
			provider, _ := cmd.Flags().GetString("ci")
			return core.SyncProjectSecrets(cmd.Context(), root, project, environment, branch, provider, repository)
		},
	}
	addSecretFlags(command)
	command.Flags().String("repository", "", "Target owner/repository (required)")
	command.Flags().String("ci", "github", "Target CI provider: github or gitlab")
	return command
}

// Private helpers ------------------------------------------------------------

func addSecretFlags(command *cobra.Command) {
	command.Flags().String("env", "dev", "Application environment: dev, stage, prod, preview")
	command.Flags().String("project", "", "Application project (required for exec and sync)")
	command.Flags().String("branch", "", "Feature branch identity for preview")
}

func secretCommandOptions(command *cobra.Command) (root, project, environment, branch string, err error) {
	root, err = premiseRoot()
	if err != nil {
		return
	}
	environment, _ = command.Flags().GetString("env")
	project, _ = command.Flags().GetString("project")
	branch, _ = command.Flags().GetString("branch")
	if environment == "preview" && branch == "" {
		branch, err = core.CurrentFeatureBranch(command.Context(), root)
	}
	return
}
