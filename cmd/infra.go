package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// InfraCommand creates the infra command.
func InfraCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "infra",
		Short: "Configure, preview, and apply Pulumi infrastructure",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(
		InfraAuthCommand(),
		InfraInitCommand(),
		InfraPlanCommand(),
		InfraApplyCommand(),
		InfraOutputCommand(),
		InfraQueryCommand(),
		InfraDestroyCommand(),
	)
	return command
}

// InfraQueryCommand creates the query command for saved resource data.
func InfraQueryCommand() *cobra.Command {
	var query core.InfraQuery
	command := &cobra.Command{
		Use:   "query",
		Short: "Read a saved resource property or exported stack output",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			options, err := resolvedInfraCommandOptions(cmd, root)
			if err != nil {
				return err
			}
			return core.QueryInfrastructure(cmd.Context(), root, options, query, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	addInfraFlags(command)
	command.Flags().StringVar(&query.Output, "output", "", "Exported stack output name")
	command.Flags().StringVar(&query.Resource, "resource", "", "Resource logical name or full Pulumi URN")
	command.Flags().StringVar(&query.Type, "type", "", "Exact resource type to disambiguate a logical name")
	command.Flags().StringVar(&query.Property, "property", "", "Resource property path, id, urn, or type")
	command.Flags().BoolVar(&query.JSON, "json", false, "Encode the selected value as JSON")
	command.Flags().BoolVar(&query.ShowSecrets, "show-secrets", false, "Explicitly decrypt and print the selected secret value")
	return command
}

// InfraInitCommand creates the init command.
func InfraInitCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize an environment stack",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "init") },
	}
	addInfraFlags(command)
	return command
}

// InfraPlanCommand creates the plan command.
func InfraPlanCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "plan",
		Aliases: []string{"preview"},
		Short:   "Preview an environment update",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "plan") },
	}
	addInfraFlags(command)
	return command
}

// InfraApplyCommand creates the apply command.
func InfraApplyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "apply",
		Short: "Apply an environment update",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "apply") },
	}
	addInfraFlags(command)
	command.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	return command
}

// InfraOutputCommand creates the output command.
func InfraOutputCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "output",
		Short: "Read environment stack outputs as JSON",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "output") },
	}
	addInfraFlags(command)
	return command
}

// InfraDestroyCommand creates the destroy command.
func InfraDestroyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy an environment stack",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "destroy") },
	}
	addInfraFlags(command)
	command.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	return command
}

// InfraAuthCommand creates the auth command.
func InfraAuthCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate declared infrastructure providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			ci, _ := cmd.Flags().GetBool("ci")
			values, err := core.AuthenticateInfrastructure(cmd.Context(), root, ci, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			path, _ := cmd.Flags().GetString("env-file")
			if path == "" {
				return nil
			}
			for key, value := range values {
				if strings.ContainsAny(key+value, "\r\n") {
					return fmt.Errorf("invalid credential path")
				}
			}
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
			if err != nil {
				return err
			}
			defer file.Close()
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, err := fmt.Fprintf(file, "%s=%s\n", key, values[key]); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().Bool("ci", false, "Use CI identity instead of interactive OAuth")
	command.Flags().String("env-file", "", "Append credential paths to the CI environment file")
	return command
}

// Private helpers ------------------------------------------------------------

func runInfraCommand(cmd *cobra.Command, args []string, operation string) error {
	root, err := premiseRoot()
	if err != nil {
		return err
	}
	options, err := resolvedInfraCommandOptions(cmd, root)
	if err != nil {
		return err
	}
	return core.RunInfra(cmd.Context(), root, operation, options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func addInfraFlags(command *cobra.Command) {
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
}

func infraCommandOptions(command *cobra.Command) core.InfraOptions {
	environment, _ := command.Flags().GetString("env")
	branch, _ := command.Flags().GetString("branch")
	shared, _ := command.Flags().GetBool("shared")
	backend, _ := command.Flags().GetString("backend")
	yes, _ := command.Flags().GetBool("yes")
	return core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, Yes: yes}
}

func resolvedInfraCommandOptions(command *cobra.Command, root string) (core.InfraOptions, error) {
	options := infraCommandOptions(command)
	if options.Environment == "preview" && options.Branch == "" {
		branch, err := core.CurrentFeatureBranch(command.Context(), root)
		if err != nil {
			return options, err
		}
		options.Branch = branch
	}
	return options, nil
}
