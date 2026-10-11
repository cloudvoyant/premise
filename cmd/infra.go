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
				if len(args) == 1 {
					options.Project = args[0]
				} else {
					options.All = true
				}
				return core.RunInfra(cmd.Context(), root, operation, options, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			},
		}
		addInfraFlags(command)
		command.Flags().Bool("all", false, "Select all infrastructure projects; apply shared first")
		if operation == "init" {
			command.Flags().String("gcp-project", "", "Google Cloud project ID")
			command.Flags().String("location", "", "Bucket location, for example US")
		}
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
		Use: "bind <app> <infra-project>", Short: "Bind an app once in template-specific project YAML", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			shared, _ := cmd.Flags().GetBool("shared")
			artifact, _ := cmd.Flags().GetString("artifact")
			return core.BindInfra(root, args[0], args[1], artifact, shared)
		},
	}
	bind.Flags().Bool("shared", false, "Bind to shared infrastructure instead of per-environment infrastructure")
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
	command.Flags().String("backend", "", "Absolute file:// backend; defaults to workspace .pulumi-state")
}

func infraCommandOptions(command *cobra.Command) core.InfraOptions {
	environment, _ := command.Flags().GetString("env")
	branch, _ := command.Flags().GetString("branch")
	shared, _ := command.Flags().GetBool("shared")
	backend, _ := command.Flags().GetString("backend")
	all, _ := command.Flags().GetBool("all")
	yes, _ := command.Flags().GetBool("yes")
	gcpProject, _ := command.Flags().GetString("gcp-project")
	location, _ := command.Flags().GetString("location")
	return core.InfraOptions{Environment: environment, Branch: branch, Shared: shared, Backend: backend, All: all, Yes: yes, GCPProject: gcpProject, Location: location}
}
