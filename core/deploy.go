package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DeployOptions identifies the environment and immutable application inputs.
type DeployOptions struct {
	InfraOptions
	Release string
}

// Public API -----------------------------------------------------------------

// RunDeployment runs the local equivalent of the separate hosted deployment steps.
// Infrastructure and migration failures stop execution without compensation.
func RunDeployment(ctx context.Context, root string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	exists, err := InfrastructureExists(ctx, root, options.InfraOptions, stderr)
	if err != nil {
		return err
	}
	if err := PrepareInfrastructure(ctx, root, options.InfraOptions, stdin, stdout, stderr); err != nil {
		return err
	}
	if exists {
		if err := RunMigrations(ctx, root, options.InfraOptions, "", stdout, stderr); err != nil {
			return err
		}
	}
	if err := RunInfra(ctx, root, "plan", options.InfraOptions, stdin, stdout, stderr); err != nil {
		return err
	}
	apply := options.InfraOptions
	apply.Yes = true
	if err := RunInfra(ctx, root, "apply", apply, stdin, stdout, stderr); err != nil {
		return err
	}
	if !exists {
		if err := RunMigrations(ctx, root, options.InfraOptions, "", stdout, stderr); err != nil {
			return err
		}
	}
	return RunApplicationDeployment(ctx, root, options, stdin, stdout, stderr)
}

// RunApplicationDeployment runs application deploy and health tasks.
// On failure, client rollback tasks use the deployment platform's revision history.
func RunApplicationDeployment(ctx context.Context, root string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	if manifest.Workspace.Deployment == nil {
		return nil
	}
	destination, err := ResolveInfrastructure(root, options.InfraOptions)
	if err != nil {
		return err
	}
	environment := map[string]string{
		"PM_ENV": options.Environment, "PM_BRANCH": options.Branch,
		"PM_STACK": destination.Stack, "PULUMI_BACKEND_URL": destination.BackendURL,
		"PM_INFRA_BACKEND": options.Backend, "PM_RELEASE": options.Release,
	}
	runner := infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}
	applications := manifest.Workspace.Deployment.Applications
	var attempted []DeploymentApplication
	rollback := func(phase string, failure error) error {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		environment["PM_FAILED_PHASE"] = phase
		failures := []error{failure}
		for index := len(attempted) - 1; index >= 0; index-- {
			app := attempted[index]
			if err := runDeploymentTask(recovery, root, manifest, taskReference{Project: app.Project, Task: app.Rollback}, environment, runner, options); err != nil {
				failures = append(failures, fmt.Errorf("rollback %s: %w", app.Project, err))
			}
		}
		return errors.Join(failures...)
	}
	for _, app := range applications {
		attempted = append(attempted, app)
		if app.Deploy != "" {
			if err := runDeploymentTask(ctx, root, manifest, taskReference{Project: app.Project, Task: app.Deploy}, environment, runner, options); err != nil {
				return rollback("deploy", err)
			}
		}
	}
	for _, app := range applications {
		if err := runDeploymentTask(ctx, root, manifest, taskReference{Project: app.Project, Task: app.Health}, environment, runner, options); err != nil {
			return rollback("health", err)
		}
	}
	return nil
}

// Private helpers ------------------------------------------------------------

type taskReference struct{ Project, Task string }

func runDeploymentTask(ctx context.Context, root string, manifest Config, ref taskReference, base map[string]string, runner infraCommandRunner, options DeployOptions) error {
	var project Project
	for _, candidate := range manifest.Workspace.Projects {
		if candidate.Name == ref.Project {
			project = candidate
			break
		}
	}
	if project.Name == "" {
		return fmt.Errorf("cannot run task %q: application %q is not declared in premise.yaml", ref.Task, ref.Project)
	}
	directory, err := TemplateDirectory(root, project.Path)
	if err != nil {
		return err
	}
	environment := withoutEnvironment(os.Environ(), "PM_FAILED_PHASE", "MISE_CEILING_PATHS")
	for key, value := range base {
		environment = withoutEnvironment(environment, key)
		environment = append(environment, key+"="+value)
	}
	environment = append(environment, "MISE_CEILING_PATHS="+filepath.Dir(root))
	sensitive := map[string]string{}
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && value != "" && deploymentSecretKeys[key] {
			sensitive[key] = value
		}
	}
	output := &secretRedactor{destination: runner.stdout, values: sensitive}
	diagnostics := &secretRedactor{destination: runner.stderr, values: sensitive}
	command := exec.CommandContext(ctx, "mise", "run", ref.Task, "--", options.Environment)
	command.Dir = directory
	command.Env = environment
	command.Stdin, command.Stdout, command.Stderr = runner.stdin, output, diagnostics
	runErr := command.Run()
	if runErr != nil {
		runErr = fmt.Errorf("run %s for %s in %s: %w", ref.Task, ref.Project, options.Environment, runErr)
	}
	return errors.Join(runErr, output.Flush(), diagnostics.Flush())
}
