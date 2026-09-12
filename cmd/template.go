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
			kind, err = askTemplateKind()
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

func init() {
	templateCmd.AddCommand(templateInitCmd, templateTestCmd)
}
