package cmd

import (
	"fmt"
	"github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
	"os/exec"
	"strings"
)

// Private helpers ------------------------------------------------------------

func init() {
	group := &cobra.Command{Use: "secrets", Short: "Resolve application secrets through fnox and Mise"}
	for _, operation := range []string{"pull", "exec"} {
		command := &cobra.Command{Use: operation, Short: "Resolve environment-scoped application secrets"}
		command.Flags().String("env", "dev", "Application environment: dev, stage, prod, preview")
		command.Flags().String("project", "", "Application project (required for exec)")
		command.Flags().String("branch", "", "Feature branch identity for preview (defaults to current branch)")
		if operation == "pull" {
			command.Args = cobra.NoArgs
		} else {
			command.Args = cobra.MinimumNArgs(1)
		}
		command.RunE = func(command *cobra.Command, arguments []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			environment, _ := command.Flags().GetString("env")
			project, _ := command.Flags().GetString("project")
			branch, _ := command.Flags().GetString("branch")
			if environment == "preview" && branch == "" {
				branch, err = currentFeatureBranch(command, root)
				if err != nil {
					return err
				}
			}
			if operation == "pull" {
				return core.PullProjectSecrets(command.Context(), root, project, environment, branch)
			}
			return core.ExecProjectSecrets(command.Context(), root, project, environment, branch, arguments, command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr())
		}
		group.AddCommand(command)
	}
	rootCmd.AddCommand(group)
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
