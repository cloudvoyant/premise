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

// NewInfraCommand creates the infra command.
func NewInfraCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "infra",
		Short: "Configure, preview, and apply Pulumi infrastructure",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	command.AddCommand(
		NewInfraAuthCommand(),
		NewInfraInitCommand(),
		NewInfraPlanCommand(),
		NewInfraApplyCommand(),
		NewInfraOutputCommand(),
		NewInfraDestroyCommand(),
		NewInfraBindCommand(),
		NewInfraTestCommand(),
	)
	return command
}

// NewInfraInitCommand creates the init command.
func NewInfraInitCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "init [project]",
		Short: "Initialize an environment stack",
		Args:  cobra.MaximumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "init") },
	}
	addInfraFlags(command)
	command.Flags().Bool("all", false, "Select the complete environment program")
	return command
}

// NewInfraPlanCommand creates the plan command.
func NewInfraPlanCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "plan [project]",
		Aliases: []string{"preview"},
		Short:   "Preview an environment update",
		Args:    cobra.MaximumNArgs(1),
		RunE:    func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "plan") },
	}
	addInfraFlags(command)
	command.Flags().Bool("all", false, "Select the complete environment program")
	return command
}

// NewInfraApplyCommand creates the apply command.
func NewInfraApplyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "apply [project]",
		Short: "Apply an environment update",
		Args:  cobra.MaximumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "apply") },
	}
	addInfraFlags(command)
	command.Flags().Bool("all", false, "Select the complete environment program")
	command.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	return command
}

// NewInfraOutputCommand creates the output command.
func NewInfraOutputCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "output [project]",
		Short: "Read environment stack outputs as JSON",
		Args:  cobra.MaximumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "output") },
	}
	addInfraFlags(command)
	command.Flags().Bool("all", false, "Select the complete environment program")
	return command
}

// NewInfraDestroyCommand creates the destroy command.
func NewInfraDestroyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "destroy [project]",
		Short: "Destroy an environment stack",
		Args:  cobra.MaximumNArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "destroy") },
	}
	addInfraFlags(command)
	command.Flags().Bool("all", false, "Select the complete environment program")
	command.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	return command
}

// NewInfraBindCommand creates the bind command.
func NewInfraBindCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "bind <app> <infra-project>",
		Short: "Bind an app artifact to its declared infrastructure program",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			artifact, _ := cmd.Flags().GetString("artifact")
			return core.BindInfra(root, args[0], args[1], artifact)
		},
	}
	command.Flags().String("artifact", "dist", "Artifact directory relative to the app")
	return command
}

// NewInfraTestCommand creates the test command.
func NewInfraTestCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "test [project]",
		Short: "Run infrastructure program and local component tests without deploying",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			project := ""
			if len(args) == 1 {
				project = args[0]
			}
			return core.TestInfrastructure(cmd.Context(), root, project, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	return command
}

// NewInfraAuthCommand creates the auth command.
func NewInfraAuthCommand() *cobra.Command {
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
	if len(args) == 1 {
		options.Project = args[0]
	} else {
		options.All = true
	}
	return core.RunInfra(cmd.Context(), root, operation, options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func addInfraFlags(command *cobra.Command) {
	command.Flags().String("env", "", "Environment: dev, stage, prod, or preview")
	command.Flags().String("branch", "", "Branch identity required for preview environments")
	command.Flags().Bool("shared", false, "Select only shared infrastructure")
	command.Flags().String("backend", "", "Named saved backend; defaults to environment/workspace selection")
	command.Flags().String("secrets-provider", "", "Pulumi encryption provider for newly created stacks (for example gcpkms://...)")
}

func infraCommandOptions(command *cobra.Command) core.InfraOptions {
	environment, _ := command.Flags().GetString("env")
	branch, _ := command.Flags().GetString("branch")
	shared, _ := command.Flags().GetBool("shared")
	backend, _ := command.Flags().GetString("backend")
	secretsProvider, _ := command.Flags().GetString("secrets-provider")
	all, _ := command.Flags().GetBool("all")
	yes, _ := command.Flags().GetBool("yes")
	return core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, SecretsProvider: secretsProvider, All: all, Yes: yes}
}

func resolvedInfraCommandOptions(command *cobra.Command, root string) (core.InfraOptions, error) {
	options := infraCommandOptions(command)
	if options.Environment == "preview" && options.Branch == "" {
		branch, err := currentFeatureBranch(command, root)
		if err != nil {
			return options, err
		}
		options.Branch = branch
	}
	return options, nil
}
