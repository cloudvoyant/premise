package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

var secretsCmd = &cobra.Command{Use: "secrets", Short: "Pull local secrets or copy application values to CI", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

var secretsPullCmd = &cobra.Command{
	Use: "pull", Short: "Write local application env files from fnox", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, project, environment, branch, err := secretCommandOptions(cmd)
		if err != nil {
			return err
		}
		return core.PullProjectSecrets(cmd.Context(), root, project, environment, branch)
	},
}

var secretsExecCmd = &cobra.Command{
	Use: "exec -- command [args...]", Short: "Run a command with environment-scoped application values", Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, project, environment, branch, err := secretCommandOptions(cmd)
		if err != nil {
			return err
		}
		return core.ExecProjectSecrets(cmd.Context(), root, project, environment, branch, args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

var secretsSyncCmd = &cobra.Command{
	Use: "sync", Short: "Copy local fnox application values to CI secrets", Args: cobra.NoArgs,
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

func init() {
	addSecretFlags(secretsPullCmd)
	addSecretFlags(secretsExecCmd)
	addSecretFlags(secretsSyncCmd)
	secretsSyncCmd.Flags().String("repository", "", "Target owner/repository (required)")
	secretsSyncCmd.Flags().String("ci", "github", "Target CI provider: github or gitlab")
	secretsCmd.AddCommand(secretsPullCmd, secretsExecCmd, secretsSyncCmd)
	rootCmd.AddCommand(secretsCmd)
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
		branch, err = currentFeatureBranch(command, root)
	}
	return
}

func currentFeatureBranch(command *cobra.Command, root string) (string, error) {
	git := exec.CommandContext(command.Context(), "git", "symbolic-ref", "--short", "HEAD")
	git.Dir = root
	output, err := git.Output()
	if err != nil {
		return "", fmt.Errorf("preview requires --branch outside a feature-branch checkout")
	}
	branch := strings.TrimSpace(string(output))
	if branch == "main" || branch == "master" {
		return "", fmt.Errorf("preview requires a feature branch")
	}
	return branch, nil
}
