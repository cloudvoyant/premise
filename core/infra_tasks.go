package core

import (
	"context"
	"errors"
	"io"
	"path/filepath"
)

// Public API -----------------------------------------------------------------

// TestInfrastructure runs project-defined tests for programs and registered
// in-repository components. Tests do not require a stack or cloud credentials.
func TestInfrastructure(ctx context.Context, root, project string, stdin io.Reader, stdout, stderr io.Writer) error {
	projects, err := infrastructureProjects(root, project, project == "", true)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return errors.New("no infrastructure programs or components to test")
	}
	for _, project := range projects {
		if _, err := TemplateDirectory(root, project.Path); err != nil {
			return err
		}
		if err := RunProjectTask(ctx, root, project.Name, "test", stdin, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}

// RunDeploymentTask is an explicit escape hatch for application-defined deploy
// tasks. It passes environment and resolved stack as arguments, without Pulumi.
func RunDeploymentTask(ctx context.Context, root, project, task string, options InfraOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	stack, err := InfraStack(options)
	if err != nil {
		return err
	}
	if project == "" || task == "" {
		return errors.New("task deployment requires a project and task")
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	for _, candidate := range manifest.Workspace.Projects {
		if candidate.Name == project {
			if _, err := TemplateDirectory(root, candidate.Path); err != nil {
				return err
			}
			environment := options.Environment
			if options.Shared {
				environment = "shared"
			}
			arguments := []string{"--", "--environment", environment, "--stack", stack}
			if options.Branch != "" {
				arguments = append(arguments, "--branch", options.Branch)
			}
			return RunProjectTask(ctx, root, project, task, stdin, stdout, stderr, arguments...)
		}
	}
	return ErrProjectNotFound
}
