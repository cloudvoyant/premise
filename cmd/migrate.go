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
			options, err := resolvedInfraCommandOptions(command, root)
			if err != nil {
				return err
			}
			return core.RunMigrations(command.Context(), root, options, group, command.OutOrStdout(), command.ErrOrStderr())
		},
	}
	addInfraFlags(command)
	command.Flags().StringVar(&group, "group", "", "Run one declared migration group")
	return command
}
