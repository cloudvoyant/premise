package cmd

import (
	"context"
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

With no selector, premise lists every official registry in an interactive
picker and records the selected entry's fully qualified selector.
Pass <owner>/<repo> to pick from one registry, or <owner>/<repo>:<template>
to name a template non-interactively. A bare :<template> shorthand resolves
the unique official match. Use .:app to select a template from the current
workspace.`,
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

		selector, err := resolveSelectorArgument(cmd.Context(), args)
		if err != nil {
			return err
		}
		return core.Generate(cmd.Context(), cwd, selector, core.InteractiveQuestionnaire{}, cmd.OutOrStdout())
	},
}

// resolveSelectorArgument routes a generate argument to the matching selector
// resolution strategy and returns the fully qualified selector to generate.
func resolveSelectorArgument(ctx context.Context, args []string) (string, error) {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	classified, err := core.ClassifyGenerateArgument(arg)
	if err != nil {
		return "", err
	}
	switch classified.Kind {
	case core.SelectorDefault:
		return core.AskDefaultTemplate(ctx)
	case core.SelectorOfficialName:
		return core.ResolveOfficialTemplateName(ctx, classified.Value)
	case core.SelectorSource:
		return core.AskRegistryTemplate(ctx, classified.Value)
	case core.SelectorExplicit:
		return classified.Value, nil
	default:
		return "", fmt.Errorf("unhandled selector kind %d", classified.Kind)
	}
}
