package cmd

import (
	"errors"
	"fmt"
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:     "generate [template]",
	Aliases: []string{"g"},
	Short:   "Generate a monorepo project from a template",
	Long: `Generates a monorepo project from a template, driven by a questionnaire.

With no selector, premise lists the default registry in an interactive picker.
A native selector such as :premise-app resolves to cloudvoyant/premise:premise-app.
Use .:app to select a template from the current workspace.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		if _, _, err := core.FindManifest(cwd); err != nil {
			if errors.Is(err, core.ErrNotPremiseProject) {
				return errors.New("Not a premise project. Run pm init to initialize a premise project.")
			}
			return err
		}

		selector := ""
		if len(args) == 1 {
			selector = args[0]
		} else {
			selector, err = askDefaultTemplate(cmd.Context())
			if err != nil {
				return err
			}
		}
		return core.Generate(cmd.Context(), cwd, selector, huhQuestionnaire{}, cmd.OutOrStdout())
	},
}
