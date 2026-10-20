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

// DeploymentPlan exposes state and recovery destinations for CI orchestration.
type DeploymentPlan struct {
	Program          string           `json:"program"`
	Stack            string           `json:"stack"`
	Backend          string           `json:"backend"`
	BackendURL       string           `json:"backend_url"`
	RecordsDirectory string           `json:"records_directory"`
	Steps            []string         `json:"steps"`
	Migrations       []MigrationGroup `json:"migrations"`
	MigrationMatrix  MigrationMatrix  `json:"migration_matrix"`
	RecordsURL       string           `json:"records_url"`
}

// Public API -----------------------------------------------------------------

// PlanDeployment returns ordered steps without contacting providers or secret stores.
func PlanDeployment(root string, options InfraOptions) ([]string, error) {
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	d := manifest.Workspace.Deployment
	if d == nil {
		return nil, fmt.Errorf("workspace.deployment is not configured")
	}
	steps := []string{"program " + destination.ProjectName + " stack " + destination.Stack + " backend " + destination.BackendName}
	if len(manifest.Workspace.Infra.Prerequisites) > 0 {
		steps = append(steps, "preview and apply database prerequisites")
	}
	groups, err := PlanMigrations(root, manifest)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		for _, project := range group.Projects {
			steps = append(steps, "migrate "+project+" for "+options.Environment)
		}
	}
	steps = append(steps, "preview and apply full environment stack")
	for _, app := range d.Applications {
		if app.Deploy != "" {
			steps = append(steps, "deploy "+app.Project+":"+app.Deploy)
		}
	}
	for _, app := range d.Applications {
		steps = append(steps, "health "+app.Project+":"+app.Health)
	}
	steps = append(steps, "persist successful release; recover automatically on failure")
	return steps, nil
}

// ResolveDeploymentPlan supplies the durable-record directory without shell YAML parsing.
func ResolveDeploymentPlan(root string, options InfraOptions) (DeploymentPlan, error) {
	steps, err := PlanDeployment(root, options)
	if err != nil {
		return DeploymentPlan{}, err
	}
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		return DeploymentPlan{}, err
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return DeploymentPlan{}, err
	}
	store, err := deploymentRecordStore(root, manifest, destination)
	if err != nil {
		return DeploymentPlan{}, err
	}
	groups, err := PlanMigrations(root, manifest)
	if err != nil {
		return DeploymentPlan{}, err
	}
	directory := ""
	if strings.HasPrefix(store.URL, "file://") {
		directory = strings.TrimPrefix(store.URL, "file://")
	}
	return DeploymentPlan{
		Program:          destination.ProjectName,
		Stack:            destination.Stack,
		Backend:          destination.BackendName,
		BackendURL:       destination.BackendURL,
		RecordsDirectory: directory,
		RecordsURL:       store.URL,
		Steps:            steps,
		Migrations:       groups,
		MigrationMatrix:  MigrationMatrix{Include: groups},
	}, nil
}

// RunDeployment runs the same deployment phases used by CI jobs and recovers on failure.
func RunDeployment(ctx context.Context, root string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if options.RunID == "" {
		options.RunID = fmt.Sprintf("local-%d", time.Now().UnixNano())
	}
	plan, err := ResolveDeploymentPlan(root, options.InfraOptions)
	if err != nil {
		return err
	}
	if err := RunDeploymentPhase(ctx, root, "start", options, stdin, stdout, stderr); err != nil {
		return err
	}
	execute := func(phase string) error {
		if err := RunDeploymentPhase(ctx, root, phase, options, stdin, stdout, stderr); err != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
			defer cancel()
			recoveryErr := RunDeploymentPhase(recovery, root, "recover", options, stdin, stdout, stderr)
			return errors.Join(fmt.Errorf("deployment %s failed during %s: %w", options.RunID, phase, err), recoveryErr)
		}
		return nil
	}
	if err := execute("provision"); err != nil {
		return err
	}
	for _, group := range plan.Migrations {
		options.Group = group.Name
		if err := execute("migrate"); err != nil {
			return err
		}
	}
	if err := execute("apply"); err != nil {
		return err
	}
	return execute("complete")
}

// Private helpers ------------------------------------------------------------

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
	case "provision":
		call(config.Recovery.Provision)
		if previous == nil && config.Recovery.Provision == nil {
			call(config.Recovery.FirstDeploy)
		}
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
