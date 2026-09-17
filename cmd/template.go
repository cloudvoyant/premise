package cmd

import (
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var templateCmd = &cobra.Command{
	Use:   "template",
	Short: "Create and test premise templates",
	Args:  cobra.NoArgs,
}

var templateInitCmd = &cobra.Command{
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

var templateDetectCmd = &cobra.Command{
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

var templateListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List templates declared by this registry",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := premiseRoot()
		if err != nil {
			return err
		}
		kind, err := core.DetectProjectKind(root)
		if err != nil {
			return err
		}
		if kind != core.ProjectKindTemplateRegistry {
			return fmt.Errorf("template ls requires a template registry, got %s", kind)
		}
		registry, err := core.LoadRegistry(root)
		if err != nil {
			return err
		}
		for _, name := range registry.Names() {
			fmt.Fprintln(cmd.OutOrStdout(), name)
		}
		return nil
	},
}

var templateTestCmd = &cobra.Command{
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

func premiseRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	root, _, err := core.FindManifest(cwd)
	return root, err
}

func init() {
	templateCmd.AddCommand(templateDetectCmd, templateInitCmd, templateListCmd, templateTestCmd)
}
