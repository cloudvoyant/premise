package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

var ErrProjectNotFound = errors.New("project not found")

// RunRootTask runs a named Mise task from the workspace root.
func RunRootTask(ctx context.Context, root, task string, stdout, stderr io.Writer, arguments ...string) error {
	return runMiseTask(ctx, root, root, task, stdout, stderr, arguments...)
}

// RunProjectTask runs a named Mise task from a generated project.
func RunProjectTask(ctx context.Context, root, projectName, task string, stdout, stderr io.Writer, arguments ...string) error {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	for _, project := range manifest.Workspace.Projects {
		if project.Name == projectName {
			return runMiseTask(ctx, root, filepath.Join(root, project.Path), task, stdout, stderr, arguments...)
		}
	}
	return fmt.Errorf("%w: %s", ErrProjectNotFound, projectName)
}

func runMiseTask(ctx context.Context, root, directory, task string, stdout, stderr io.Writer, arguments ...string) error {
	if task == "" {
		return errors.New("task name is required")
	}
	runner := miseRunner{Stdout: stdout, Stderr: stderr, Ceiling: filepath.Dir(root)}
	miseArguments := append([]string{"run", task}, arguments...)
	if err := runner.run(ctx, directory, nil, miseArguments...); err != nil {
		return fmt.Errorf("mise run %s: %w", task, err)
	}
	return nil
}

var commonTasks = []string{
	"install",
	"build",
	"clean",
	"test",
	"lint",
	"lint:fix",
	"format",
	"format:check",
	"env-pull",
	"publish:rc",
	"publish",
}

var appTasks = []string{
	"run",
	"dev",
	"deploy",
	"e2e",
}

func ContractTasks(kind string) ([]string, error) {
	if _, err := KindDirectory(kind); err != nil {
		return nil, err
	}
	tasks := append([]string{}, commonTasks...)
	if kind == "app" {
		tasks = append(tasks, appTasks...)
	}
	return tasks, nil
}
