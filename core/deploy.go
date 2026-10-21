package core

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DeployOptions pins an immutable release and its artifact identities.
type DeployOptions struct {
	InfraOptions
	RunID          string
	Group          string
	Release        string
	Artifacts      map[string]string
	WorkersStopped bool
}

// DeploymentResult records both the original failure and recovery outcome.
type DeploymentResult struct {
	Release           string `json:"release"`
	Phase             string `json:"phase"`
	Succeeded         bool   `json:"succeeded"`
	RecoveryAttempted bool   `json:"recovery_attempted"`
	RecoverySucceeded bool   `json:"recovery_succeeded"`
}

// Public API -----------------------------------------------------------------

// RunDeployment runs the same deployment phases used by CI jobs and recovers on failure.
func RunDeployment(ctx context.Context, root string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if options.RunID == "" {
		options.RunID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	groups, err := PlanMigrations(root, manifest)
	if err != nil {
		return err
	}
	if err := runDeploymentPhase(ctx, root, "start", options, stdin, stdout, stderr); err != nil {
		return err
	}
	execute := func(phase string) error {
		if err := runDeploymentPhase(ctx, root, phase, options, stdin, stdout, stderr); err != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
			defer cancel()
			recoveryErr := runDeploymentPhase(recovery, root, "recover", options, stdin, stdout, stderr)
			return errors.Join(fmt.Errorf("deployment %s failed during %s: %w", options.RunID, phase, err), recoveryErr)
		}
		return nil
	}
	exists, err := InfrastructureExists(ctx, root, options.InfraOptions, stderr)
	if err != nil {
		return executeFailure(ctx, root, options, "inspect", err, stdin, stdout, stderr)
	}
	if err := execute("prepare"); err != nil {
		return err
	}
	migrate := func() error {
		stdout, stderr := synchronizedDeploymentWriters(stdout, stderr)
		var workers sync.WaitGroup
		var failures []error
		var mutex sync.Mutex
		for _, group := range groups {
			workers.Add(1)
			go func(group MigrationGroup) {
				defer workers.Done()
				selected := options
				selected.Group = group.Name
				if err := runDeploymentPhase(ctx, root, "migrate", selected, stdin, stdout, stderr); err != nil {
					mutex.Lock()
					failures = append(failures, err)
					mutex.Unlock()
				}
			}(group)
		}
		workers.Wait()
		return errors.Join(failures...)
	}
	if exists {
		if err := migrate(); err != nil {
			return executeFailure(ctx, root, options, "migration", err, stdin, stdout, stderr)
		}
	}
	if err := execute("update"); err != nil {
		return err
	}
	if !exists {
		if err := migrate(); err != nil {
			return executeFailure(ctx, root, options, "migration", err, stdin, stdout, stderr)
		}
	}

	return execute("complete")
}

// ParseArtifactReferences accepts the JSON map or project=reference pairs used by CI.
func ParseArtifactReferences(value string) (map[string]string, error) {
	artifacts := map[string]string{}
	if strings.TrimSpace(value) == "" {
		return artifacts, nil
	}
	if strings.HasPrefix(strings.TrimSpace(value), "{") {
		err := json.Unmarshal([]byte(value), &artifacts)
		return artifacts, err
	}
	entries, err := csv.NewReader(strings.NewReader(value)).Read()
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name, reference, ok := strings.Cut(entry, "=")
		if !ok || name == "" || reference == "" {
			return nil, fmt.Errorf("artifact references must be project=reference pairs")
		}
		if _, exists := artifacts[name]; exists {
			return nil, fmt.Errorf("duplicate artifact for %s", name)
		}
		artifacts[name] = reference
	}
	return artifacts, nil
}

// RunApplicationDeployment deploys and checks application artifacts without running migrations or updating infrastructure.
func RunApplicationDeployment(ctx context.Context, root string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if options.RunID == "" {
		options.RunID = fmt.Sprintf("deploy-%d", time.Now().UnixNano())
	}
	if err := runDeploymentPhase(ctx, root, "start", options, stdin, stdout, stderr); err != nil {
		return err
	}
	if err := runDeploymentPhase(ctx, root, "complete", options, stdin, stdout, stderr); err != nil {
		return executeFailure(ctx, root, options, "deploy", err, stdin, stdout, stderr)
	}
	return nil
}

// Private helpers ------------------------------------------------------------

func executeFailure(ctx context.Context, root string, options DeployOptions, phase string, failure error, stdin io.Reader, stdout, stderr io.Writer) error {
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer cancel()
	err := runDeploymentPhase(recovery, root, "recover", options, stdin, stdout, stderr)
	return errors.Join(fmt.Errorf("deployment %s failed during %s: %w", options.RunID, phase, failure), err)
}

func runDeploymentTask(ctx context.Context, root string, manifest Config, ref TaskReference, base map[string]string, runner infraCommandRunner, options DeployOptions) error {
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
	environment := withoutEnvironment(os.Environ(), "PM_PREVIOUS_RELEASE", "PM_PREVIOUS_ARTIFACTS", "PM_PREVIOUS_OUTPUTS", "PM_FAILED_PHASE", "MISE_CEILING_PATHS")
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
	if project.Secrets != nil {
		for _, key := range project.Secrets.Keys {
			if value := os.Getenv(key); value != "" {
				sensitive[key] = value
			}
		}
	}
	output := &secretRedactor{destination: runner.stdout, values: sensitive}
	diagnostics := &secretRedactor{destination: runner.stderr, values: sensitive}
	command := exec.CommandContext(ctx, "mise", "run", ref.Task, "--", options.Environment)
	command.Dir = directory
	command.Env = withEmptyPulumiPassphrase(environment)
	command.Stdin, command.Stdout, command.Stderr = runner.stdin, output, diagnostics
	runErr := command.Run()
	if runErr != nil {
		runErr = fmt.Errorf("run %s for %s in %s: %w", ref.Task, ref.Project, options.Environment, runErr)
	}
	return errors.Join(runErr, output.Flush(), diagnostics.Flush())
}

func recoverDeployment(ctx context.Context, runner infraRunner, d InfraResolution, config *Deployment, previous *ReleaseRecord, phase string, task func(TaskReference) error, env map[string]string) error {
	if phase == "init" {
		return nil
	}
	var failures []error
	call := func(ref *TaskReference) {
		if ref != nil {
			if err := task(*ref); err != nil {
				failures = append(failures, fmt.Errorf("recovery task failed: %w", err))
			}
		}
	}
	switch phase {
	case "migration":
		call(config.Recovery.Migration)
		if previous == nil && config.Recovery.Migration == nil {
			call(config.Recovery.FirstDeploy)
		}
	default:
		// Refresh inspects partial infrastructure changes. Rollback tasks own application
		// restoration and must preview their targeted operation before changing resources.
		if err := executeInfra(ctx, runner, d, "refresh", true, env); err != nil {
			failures = append(failures, fmt.Errorf("recovery refresh failed: %w", err))
		}
		if previous == nil {
			call(config.Recovery.FirstDeploy)
		} else {
			for _, app := range config.Applications {
				ref := TaskReference{Project: app.Project, Task: app.Rollback}
				call(&ref)
			}
		}
		call(config.Recovery.Database)
	}
	return errors.Join(failures...)
}
