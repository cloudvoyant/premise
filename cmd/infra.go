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

func InfraCommand(scaffold core.WorkspaceScaffold) *cobra.Command {
	command := &cobra.Command{
		Use:   "infra",
		Short: "Configure, preview, and apply Pulumi infrastructure",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(
		InfraSetupCommand(scaffold),
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

func InfraSetupCommand(scaffold core.WorkspaceScaffold) *cobra.Command {
	var setup core.InfrastructureSetup
	command := &cobra.Command{
		Use:   "setup",
		Short: "Add infrastructure, or prepare an existing program with --env",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("env") {
				environment, _ := cmd.Flags().GetString("env")
				branch, _ := cmd.Flags().GetString("branch")
				shared, _ := cmd.Flags().GetBool("shared")
				backend, _ := cmd.Flags().GetString("backend")
				yes, _ := cmd.Flags().GetBool("yes")
				options, err := core.ResolveInfraOptions(cmd.Context(), root, core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, Yes: yes})
				if err != nil {
					return err
				}
				return core.PrepareInfrastructure(cmd.Context(), root, options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			if err := core.SetupInfrastructure(cmd.Context(), root, scaffold, setup, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Infrastructure setup complete. Configure a backend before initializing an environment stack.")
			return nil
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	command.Flags().StringVar(&setup.Language, "language", "", "Official Pulumi language starter; existing programs supply their runtime")
	command.Flags().StringVar(&setup.PulumiVersion, "pulumi-version", "", "Pulumi version to pin; defaults to the existing pin or tested version")
	return command
}

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
			environment, _ := cmd.Flags().GetString("env")
			branch, _ := cmd.Flags().GetString("branch")
			shared, _ := cmd.Flags().GetBool("shared")
			backend, _ := cmd.Flags().GetString("backend")
			yes, _ := cmd.Flags().GetBool("yes")
			options, err := core.ResolveInfraOptions(cmd.Context(), root, core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, Yes: yes})
			if err != nil {
				return err
			}
			return core.QueryInfrastructure(cmd.Context(), root, options, query, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	command.Flags().StringVar(&query.Output, "output", "", "Exported stack output name")
	command.Flags().StringVar(&query.Resource, "resource", "", "Resource logical name or full Pulumi URN")
	command.Flags().StringVar(&query.Type, "type", "", "Exact resource type to disambiguate a logical name")
	command.Flags().StringVar(&query.Property, "property", "", "Resource property path, id, urn, or type")
	command.Flags().BoolVar(&query.JSON, "json", false, "Encode the selected value as JSON")
	command.Flags().BoolVar(&query.ShowSecrets, "show-secrets", false, "Explicitly decrypt and print the selected secret value")
	return command
}

func InfraInitCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize an environment stack",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
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
			return core.RunInfra(cmd.Context(), root, "init", options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	return command
}

func InfraPlanCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "plan",
		Aliases: []string{"preview"},
		Short:   "Preview an environment update",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
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
			return core.RunInfra(cmd.Context(), root, "plan", options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	return command
}

func InfraApplyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "apply",
		Short: "Apply an environment update",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
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
			return core.RunInfra(cmd.Context(), root, "apply", options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	command.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	return command
}

func InfraOutputCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "output",
		Short: "Read environment stack outputs as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
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
			return core.RunInfra(cmd.Context(), root, "output", options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	return command
}

func InfraDestroyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy an environment stack",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
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
			return core.RunInfra(cmd.Context(), root, "destroy", options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	command.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	return command
}

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
