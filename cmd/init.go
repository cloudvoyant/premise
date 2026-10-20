package cmd

import (
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// InitCommand creates the init command.
func InitCommand(scaffold core.WorkspaceScaffold) *cobra.Command {
	var projectKind string
	var infrastructure bool
	var setup core.InfrastructureSetup
	command := &cobra.Command{
		Use:   "init",
		Short: "Initialize a Premise project",
		Long:  `Creates either a monorepo workspace or a template registry. Existing files are preserved.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current directory: %w", err)
			}
			var path string
			switch projectKind {
			case "monorepo":
				if !cmd.Flags().Changed("infra") && setup.Language == "" && setup.PulumiVersion == "" && term.IsTerminal(os.Stdin.Fd()) {
					infrastructure, setup, err = core.AskInfrastructureSetup(cwd)
					if err != nil {
						return err
					}
				}
				if infrastructure || setup.Language != "" || setup.PulumiVersion != "" {
					path, err = core.InitializeWorkspaceWithInfrastructure(cmd.Context(), cwd, scaffold, setup, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
				} else {
					path, err = core.InitializeWorkspace(cwd, scaffold.MiseTemplate, scaffold.WorkflowAssets...)
				}
			case "template-registry":
				if infrastructure || setup.Language != "" || setup.PulumiVersion != "" {
					return fmt.Errorf("infrastructure setup is available for monorepo workspaces")
				}
				path, err = core.InitializeTemplateRegistry(cwd)
			default:
				return fmt.Errorf("invalid project kind %q: expected monorepo or template-registry", projectKind)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s\n", path)
			return nil
		},
	}
	command.Flags().StringVar(&projectKind, "kind", "monorepo", "project kind: monorepo or template-registry")
	command.Flags().BoolVar(&infrastructure, "infra", false, "Initialize or adopt a client-owned Pulumi project in infra/")
	command.Flags().StringVar(&setup.Language, "infra-language", "", "Official Pulumi language starter, such as go, typescript, or python")
	command.Flags().StringVar(&setup.PulumiVersion, "pulumi-version", "", "Pulumi version to pin in mise.toml; defaults to the existing pin or tested version")
	return command
}
