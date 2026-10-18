package cmd

import (
	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

var infraCmd = &cobra.Command{
	Use: "infra", Short: "Configure, preview, and apply Pulumi infrastructure",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

var infraInitCmd = &cobra.Command{
	Use: "init [project]", Short: "Initialize an environment stack", Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "init") },
}

var infraPlanCmd = &cobra.Command{
	Use: "plan [project]", Aliases: []string{"preview"}, Short: "Preview an environment update", Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "plan") },
}

var infraApplyCmd = &cobra.Command{
	Use: "apply [project]", Short: "Apply an environment update", Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "apply") },
}

var infraOutputCmd = &cobra.Command{
	Use: "output [project]", Short: "Read environment stack outputs as JSON", Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "output") },
}

var infraDestroyCmd = &cobra.Command{
	Use: "destroy [project]", Short: "Destroy an environment stack", Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runInfraCommand(cmd, args, "destroy") },
}

var infraBindCmd = &cobra.Command{
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

var infraTestCmd = &cobra.Command{
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
}

// Private helpers ------------------------------------------------------------

func init() {
	addInfraFlags(infraInitCmd)
	infraInitCmd.Flags().Bool("all", false, "Select the complete environment program")
	addInfraFlags(infraPlanCmd)
	infraPlanCmd.Flags().Bool("all", false, "Select the complete environment program")
	addInfraFlags(infraApplyCmd)
	infraApplyCmd.Flags().Bool("all", false, "Select the complete environment program")
	infraApplyCmd.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	addInfraFlags(infraOutputCmd)
	infraOutputCmd.Flags().Bool("all", false, "Select the complete environment program")
	addInfraFlags(infraDestroyCmd)
	infraDestroyCmd.Flags().Bool("all", false, "Select the complete environment program")
	infraDestroyCmd.Flags().Bool("yes", false, "Approve the Pulumi update non-interactively")
	infraBindCmd.Flags().String("artifact", "dist", "Artifact directory relative to the app")
	infraCmd.AddCommand(infraInitCmd, infraPlanCmd, infraApplyCmd, infraOutputCmd, infraDestroyCmd, infraBindCmd, infraTestCmd)
	rootCmd.AddCommand(infraCmd)
}

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
