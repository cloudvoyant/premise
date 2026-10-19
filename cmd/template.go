package cmd

import (
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// NewTemplateCommand creates the template command.
func NewTemplateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "template",
		Short: "Create and test premise templates",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(NewTemplateDetectCommand(), NewTemplateInitCommand(), NewTemplateListCommand(), NewTemplateTestCommand())
	return command
}

// NewTemplateInitCommand creates the init command.
func NewTemplateInitCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "init [app|lib]",
		Short: "Initialize a live template",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := ""
			if len(args) == 1 {
				kind = args[0]
			} else {
				var err error
				kind, err = core.AskTemplateKind()
				if err != nil {
					return err
				}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current directory: %w", err)
			}
			root, _, err := core.FindManifest(cwd)
			if err != nil {
				return err
			}
			path, err := core.InitializeTemplate(root, kind)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s template at %s\n", kind, path)
			return nil
		},
	}

	return command
}

// NewTemplateDetectCommand creates the detect command.
func NewTemplateDetectCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "detect",
		Short: "Print the repository lifecycle kind",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			kind, err := core.DetectProjectKind(root)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), kind)
			return nil
		},
	}

	return command
}

// NewTemplateListCommand creates the ls command.
func NewTemplateListCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List templates declared by this registry",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			registry, err := core.LoadRegistry(root)
			if err != nil {
				return err
			}
			for _, name := range registry.GetTemplateNames() {
				fmt.Fprintln(cmd.OutOrStdout(), name)
			}
			return nil
		},
	}

	return command
}

// NewTemplateTestCommand creates the test command.
func NewTemplateTestCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "test",
		Short: "Execute every template task contract",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get current directory: %w", err)
			}
			root, manifestPath, err := core.FindManifest(cwd)
			if err != nil {
				return err
			}
			manifest, err := core.LoadManifest(manifestPath)
			if err != nil {
				return err
			}
			return core.TestTemplateContracts(cmd.Context(), root, manifest, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	return command
}

// Private helpers ------------------------------------------------------------

func premiseRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	root, _, err := core.FindManifest(cwd)
	return root, err
}
