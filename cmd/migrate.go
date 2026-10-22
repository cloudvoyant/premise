package cmd

import (
	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// MigrateCommand runs optional application migrations for an environment.
func MigrateCommand() *cobra.Command {
	var group string
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Run optional application migration tasks",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			environment, _ := command.Flags().GetString("env")
			branch, _ := command.Flags().GetString("branch")
			shared, _ := command.Flags().GetBool("shared")
			backend, _ := command.Flags().GetString("backend")
			yes, _ := command.Flags().GetBool("yes")
			options, err := core.ResolveInfraOptions(command.Context(), root, core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, Yes: yes})
			if err != nil {
				return err
			}
			return core.RunMigrations(command.Context(), root, options, group, command.OutOrStdout(), command.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	command.Flags().StringVar(&group, "group", "", "Run one declared migration group")
	return command
}
