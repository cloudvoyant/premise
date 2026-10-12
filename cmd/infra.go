package cmd

import (
	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Private helpers ------------------------------------------------------------

var infraCmd = &cobra.Command{Use: "infra", Short: "Configure, preview, and apply Pulumi infrastructure"}

func init() {
	for _, operation := range []string{"init", "preview", "apply", "output", "destroy"} {
		command := &cobra.Command{
			Use: operation + " [project]", Args: cobra.MaximumNArgs(1),
			Short: "Run infrastructure " + operation + " for an explicit environment",
			RunE: func(cmd *cobra.Command, args []string) error {
				root, err := premiseRoot()
				if err != nil {
					return err
				}
				options := infraCommandOptions(cmd)
				if options.Environment == "preview" && options.Branch == "" {
					options.Branch, err = currentFeatureBranch(cmd, root)
					if err != nil {
						return err
					}
				}
				if len(args) == 1 {
					options.Project = args[0]
				} else {
					options.All = true
				}
				return core.RunInfra(cmd.Context(), root, operation, options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			},
		}
		addInfraFlags(command)
		command.Flags().Bool("all", false, "Select the complete environment program")
		if operation == "apply" || operation == "destroy" {
			command.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
		}
		if operation == "preview" {
			command.Use = "plan [project]"
			command.Aliases = []string{"preview"}
		}
		infraCmd.AddCommand(command)
	}
	bind := &cobra.Command{
		Use: "bind <app> <infra-project>", Short: "Bind an app artifact to its declared infrastructure program", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			artifact, _ := cmd.Flags().GetString("artifact")
			return core.BindInfra(root, args[0], args[1], artifact)
		},
	}
	bind.Flags().String("artifact", "dist", "Artifact directory relative to the app")
	infraCmd.AddCommand(bind)
	infraCmd.AddCommand(&cobra.Command{
		Use: "test [project]", Short: "Run infrastructure program and local component tests without deploying", Args: cobra.MaximumNArgs(1),
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
	})
	rootCmd.AddCommand(infraCmd)
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
	all, _ := command.Flags().GetBool("all")
	yes, _ := command.Flags().GetBool("yes")
	return core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, All: all, Yes: yes}
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
