package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Questionnaire interface {
	Ask([]Question) (map[string]string, error)
}

type GenerateOptions struct {
	Questionnaire Questionnaire
	Resolver      MergeResolver
}

func Generate(ctx context.Context, cwd, selector string, options GenerateOptions, output io.Writer) error {
	if options.Questionnaire == nil {
		return errors.New("generate questionnaire is required")
	}
	if options.Resolver == nil {
		return errors.New("generate merge resolver is required")
	}
	workspaceRoot, workspaceManifestPath, err := FindManifest(cwd)
	if err != nil {
		return err
	}
	workspaceManifest, err := LoadManifest(workspaceManifestPath)
	if err != nil {
		return err
	}

	sourceRoot, selection, err := ResolveTemplateSource(ctx, workspaceRoot, selector)
	if err != nil {
		return err
	}
	templateManifestPath := filepath.Join(sourceRoot, ManifestFilename)
	templateManifest, err := LoadManifest(templateManifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("template source %q does not contain %s; choose a Premise template repository or run `pm init` in that directory", sourceRoot, ManifestFilename)
	}
	if err != nil {
		return fmt.Errorf("load template manifest: %w", err)
	}
	template, err := templateManifest.FindTemplate(selection.Name)
	if err != nil {
		return err
	}
	answers, err := options.Questionnaire.Ask(template.Questions)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(answers["name"])
	if err := ValidateProjectName(name); err != nil {
		return fmt.Errorf("project name: %w", err)
	}
	answers["name"] = name

	projectDirectory, err := KindDirectory(template.Kind)
	if err != nil {
		return err
	}
	source, err := TemplateDirectory(sourceRoot, template.Name)
	if err != nil {
		return err
	}
	relativeDestination := filepath.Join(projectDirectory, name)
	destination := filepath.Join(workspaceRoot, relativeDestination)
	replacements, err := ResolveSubstitutions(template, answers)
	if err != nil {
		return err
	}
	prepared, err := PrepareScaffold(ScaffoldRequest{
		SharedSource:     filepath.Join(sourceRoot, "templates"),
		Source:           source,
		Destination:      destination,
		Replacements:     replacements,
		SelectedIdentity: selector,
		Resolver:         options.Resolver,
	})
	if err != nil {
		return err
	}
	defer prepared.Close()

	printTemplateComparison(output, prepared.Plan)
	if err := ValidateToolChanges(ctx, prepared.SelectedStage, template.Kind, prepared.Mise.ToolChanges, output); err != nil {
		return err
	}
	if err := prepared.Materialize(); err != nil {
		return err
	}
	if err := ValidateGeneratedCandidate(ctx, prepared.Stage, template.Kind, output); err != nil {
		return err
	}
	if err := prepared.Commit(); err != nil {
		return err
	}

	project := Project{
		Name:     name,
		Template: selector,
		Version:  template.Version,
		Path:     filepath.ToSlash(relativeDestination),
		Answers:  answers,
	}
	if err := workspaceManifest.AddProject(project); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	if err := SaveManifest(workspaceManifestPath, workspaceManifest); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	fmt.Fprintf(output, "Generated %s %s from %s at %s\n", template.Kind, name, selector, relativeDestination)
	return nil
}

func printTemplateComparison(output io.Writer, plan TemplateMergePlan) {
	fmt.Fprintln(output, "Template root comparison:")
	if len(plan.Collisions) == 0 {
		fmt.Fprintln(output, "- no collisions")
		return
	}
	for _, entry := range plan.Entries {
		if !entry.Shared.Present || !entry.Selected.Present {
			continue
		}
		fmt.Fprintf(output, "- %s: %s\n", entry.Path, mergeStrategyLabel(entry))
	}
}

func mergeStrategyLabel(entry MergeEntry) string {
	switch entry.Strategy {
	case MergeStrategyEqual:
		return "equal"
	case MergeStrategyOrderedLines:
		return "smart line merge"
	case MergeStrategyMise:
		return "semantic Mise merge"
	case MergeStrategyWholeFile:
		return "whole-file decision"
	default:
		return string(entry.Strategy)
	}
}
