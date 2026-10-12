package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// DeployOptions pins an immutable release and its artifact identities.
type DeployOptions struct {
	InfraOptions
	Release   string
	Artifacts map[string]string
}

// ReleaseRecord contains recovery inputs, never secrets or database outputs.
type ReleaseRecord struct {
	Release   string            `json:"release"`
	Artifacts map[string]string `json:"artifacts"`
	Program   string            `json:"program"`
	Stack     string            `json:"stack"`
	Backend   string            `json:"backend"`
	Config    map[string]string `json:"config"`
	Completed time.Time         `json:"completed"`
	Outputs   map[string]string `json:"outputs,omitempty"`
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
	Program          string   `json:"program"`
	Stack            string   `json:"stack"`
	Backend          string   `json:"backend"`
	BackendURL       string   `json:"backend_url"`
	RecordsDirectory string   `json:"records_directory"`
	Steps            []string `json:"steps"`
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
	steps := []string{"program " + destination.Program.Name + " stack " + destination.Stack + " backend " + destination.BackendName}
	if len(d.Prerequisites) > 0 {
		steps = append(steps, "preview and apply database prerequisites")
	}
	for _, task := range d.Migrations {
		steps = append(steps, "migrate "+task.Project+":"+task.Task)
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
	directory, err := deploymentRecordDirectory(root, manifest, destination)
	if err != nil {
		return DeploymentPlan{}, err
	}
	return DeploymentPlan{Program: destination.Program.Name, Stack: destination.Stack, Backend: destination.BackendName, BackendURL: destination.BackendURL, RecordsDirectory: directory, Steps: steps}, nil
}

// RunDeployment serializes both applies, migrations and recovery under one lock.
func RunDeployment(ctx context.Context, root string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(options.Artifacts) == 0 {
		return fmt.Errorf("deployment requires pinned --artifact identities")
	}
	if options.Release == "" {
		return fmt.Errorf("deployment requires immutable --release identity")
	}
	if options.Project != "" || options.Shared {
		return fmt.Errorf("deployment updates the complete environment stack")
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	declaration := manifest.Workspace.Deployment
	if declaration == nil {
		return fmt.Errorf("workspace.deployment is not configured")
	}
	destination, err := ResolveInfrastructure(root, options.InfraOptions)
	if err != nil {
		return err
	}
	records, err := deploymentRecordDirectory(root, manifest, destination)
	if err != nil {
		return err
	}
	for _, app := range declaration.Applications {
		if options.Artifacts[app.Project] == "" {
			return fmt.Errorf("app %s requires pinned artifact identity", app.Project)
		}
	}
	if err := os.MkdirAll(records, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(records, "lock"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("deployment lock unavailable; inspect interrupted run before removing lock: %w", err)
	}
	lock.Close()
	defer os.Remove(filepath.Join(records, "lock"))
	var previous *ReleaseRecord
	data, err := os.ReadFile(filepath.Join(records, "last-success.json"))
	if err == nil {
		previous = &ReleaseRecord{}
		if err := json.Unmarshal(data, previous); err != nil {
			return fmt.Errorf("decode previous release: %w", err)
		}
		if previous.Program != destination.Program.Name || previous.Stack != destination.Stack || previous.Backend != destination.BackendURL {
			return fmt.Errorf("previous release program/backend/stack mismatch; state migration is not automatic")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	secrets, err := infrastructureSecrets(ctx, root, destination, options.InfraOptions)
	if err != nil {
		return err
	}
	runner := infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr, secrets: secrets}
	result := DeploymentResult{Release: options.Release}
	env := map[string]string{"PM_ENV": options.Environment, "PM_BRANCH": options.Branch, "PM_STACK": destination.Stack, "PM_RELEASE": options.Release, "PULUMI_BACKEND_URL": destination.BackendURL}
	artifactJSON, _ := json.Marshal(options.Artifacts)
	env["PM_ARTIFACTS"] = string(artifactJSON)
	if previous != nil {
		env["PM_PREVIOUS_RELEASE"] = previous.Release
		b, _ := json.Marshal(previous.Artifacts)
		env["PM_PREVIOUS_ARTIFACTS"] = string(b)
		previousOutputs, _ := json.Marshal(previous.Outputs)
		env["PM_PREVIOUS_OUTPUTS"] = string(previousOutputs)
	}
	runTask := func(ref TaskReference) error {
		return runDeploymentTask(ctx, root, manifest, ref, env, runner, options)
	}
	infraEnv := map[string]string{}
	for key, value := range env {
		infraEnv[key] = value
	}
	for key, value := range secrets {
		infraEnv[key] = value
	}
	runInfra := func(phase string, targets []string) error {
		d := destination
		d.Targets = targets
		result.Phase = phase
		if err := executeInfra(ctx, runner, d, "preview", false, infraEnv); err != nil {
			return err
		}
		return executeInfra(ctx, runner, d, "apply", true, infraEnv)
	}
	var releaseOutputs map[string]string
	execute := func() error {
		result.Phase = "init"
		if err := executeInfra(ctx, runner, destination, "init", true, env); err != nil {
			return err
		}
		if len(declaration.Prerequisites) > 0 {
			result.Phase = "provision"
			targets, err := resolvePrerequisiteTargets(ctx, runner, destination, declaration.Prerequisites, infraEnv)
			if err != nil {
				return err
			}
			if err := runInfra("provision", targets); err != nil {
				return err
			}
		}
		result.Phase = "migration"
		for _, task := range declaration.Migrations {
			if err := runTask(task); err != nil {
				return err
			}
		}
		if err := runInfra("apply", nil); err != nil {
			return err
		}
		result.Phase = "deploy"
		for _, app := range declaration.Applications {
			if app.Deploy != "" {
				if err := runTask(TaskReference{Project: app.Project, Task: app.Deploy, administration: true, Outputs: app.Outputs}); err != nil {
					return err
				}
			}
		}
		result.Phase = "health"
		for _, app := range declaration.Applications {
			if err := runTask(TaskReference{Project: app.Project, Task: app.Health, Outputs: app.Outputs}); err != nil {
				return err
			}
		}
		result.Phase = "record"
		if len(declaration.RecordOutputs) > 0 {
			outputs, err := readReleaseOutputs(ctx, runner, destination, declaration.RecordOutputs, env)
			if err != nil {
				return err
			}
			releaseOutputs = outputs
		}
		return nil
	}
	original := execute()
	if original != nil {
		result.RecoveryAttempted = true
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		ctx = recoveryCtx
		env["PM_FAILED_PHASE"] = result.Phase
		recoveryErr := recoverDeployment(ctx, runner, destination, declaration, previous, result.Phase, runTask, env)
		result.RecoverySucceeded = recoveryErr == nil
		recordErr := writeDeploymentJSON(filepath.Join(records, "last-run.json"), result)
		return errors.Join(fmt.Errorf("deployment failed during %s: %w", result.Phase, original), recoveryErr, recordErr)
	}
	result.Succeeded = true
	record := ReleaseRecord{Program: destination.Program.Name, Release: options.Release, Artifacts: options.Artifacts, Stack: destination.Stack, Backend: destination.BackendURL, Config: destination.Config, Completed: time.Now().UTC(), Outputs: releaseOutputs}
	if err := writeDeploymentJSON(filepath.Join(records, "last-success.json"), record); err != nil {
		result.Succeeded = false
		result.Phase = "record"
		result.RecoveryAttempted = true
		env["PM_FAILED_PHASE"] = "record"
		recoveryErr := recoverDeployment(ctx, runner, destination, declaration, previous, "record", runTask, env)
		result.RecoverySucceeded = recoveryErr == nil
		return errors.Join(err, recoveryErr, writeDeploymentJSON(filepath.Join(records, "last-run.json"), result))
	}
	return writeDeploymentJSON(filepath.Join(records, "last-run.json"), result)
}

// Private helpers ------------------------------------------------------------

var recordNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func runDeploymentTask(ctx context.Context, root string, manifest Config, ref TaskReference, base map[string]string, runner infraCommandRunner, options DeployOptions) error {
	var project Project
	for _, p := range manifest.Workspace.Projects {
		if p.Name == ref.Project {
			project = p
		}
	}
	if project.Name == "" {
		return fmt.Errorf("task project %s missing", ref.Project)
	}
	env := map[string]string{}
	sensitive := map[string]string{}
	for k, v := range base {
		env[k] = v
	}
	// Recovery must work even when provisioning never created the application secrets.
	if project.Secrets != nil && base["PM_FAILED_PHASE"] == "" {
		values, err := LoadProjectSecrets(ctx, root, ref.Project, options.Environment, options.Branch)
		if err != nil {
			return err
		}
		for k, v := range values {
			env[k] = v
			sensitive[k] = v
		}
	}
	if len(ref.Outputs) > 0 {
		destination, err := ResolveInfrastructure(root, options.InfraOptions)
		if err != nil {
			return err
		}
		env["PULUMI_BACKEND_URL"] = destination.BackendURL
		var output bytes.Buffer
		private := infraCommandRunner{stdout: &output, stderr: io.Discard, private: true}
		if err := private.run(ctx, destination.Directory, env, "pulumi", "stack", "output", "--stack", destination.Stack, "--json", "--show-secrets", "--non-interactive"); err != nil {
			return fmt.Errorf("read task outputs failed")
		}
		values := map[string]any{}
		if err := json.Unmarshal(output.Bytes(), &values); err != nil {
			return fmt.Errorf("invalid task output JSON")
		}
		for key, name := range ref.Outputs {
			value, ok := values[name]
			if !ok {
				return fmt.Errorf("required stack output %s missing", name)
			}
			text, ok := value.(string)
			if !ok || text == "" {
				return fmt.Errorf("task output %s must be a non-empty string", name)
			}
			if old, ok := env[key]; ok && old != text {
				return fmt.Errorf("conflicting task input %s", key)
			}
			env[key] = text
			sensitive[key] = text
		}
	}
	directory, err := TemplateDirectory(root, project.Path)
	if err != nil {
		return err
	}
	output := &secretRedactor{destination: runner.stdout, values: sensitive}
	diagnostics := &secretRedactor{destination: runner.stderr, values: sensitive}
	command := exec.CommandContext(ctx, "mise", "run", ref.Task)
	command.Dir = directory
	command.Env = appSecretEnvironment(os.Environ(), env)
	if ref.administration {
		for _, entry := range os.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if ok && value != "" && deploymentSecretKeys[key] {
				sensitive[key] = value
			}
		}
		command.Env = os.Environ()
		for k, v := range env {
			prefix := k + "="
			command.Env = slices.DeleteFunc(command.Env, func(entry string) bool { return strings.HasPrefix(entry, prefix) })
			command.Env = append(command.Env, prefix+v)
		}
	}
	command.Stdin = runner.stdin
	command.Stdout = output
	command.Stderr = diagnostics
	err = command.Run()
	return errors.Join(err, output.Flush(), diagnostics.Flush())
}
func recoverDeployment(ctx context.Context, runner infraRunner, d InfraResolution, config *Deployment, previous *ReleaseRecord, phase string, task func(TaskReference) error, env map[string]string) error {
	if phase == "init" {
		return nil
	}
	var failures []error
	call := func(ref *TaskReference) {
		if ref != nil {
			ref.administration = true
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
				ref := TaskReference{Project: app.Project, Task: app.Rollback, administration: true, Outputs: app.Outputs}
				call(&ref)
			}
		}
		call(config.Recovery.Database)
	}
	return errors.Join(failures...)
}
func writeDeploymentJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func deploymentRecordDirectory(root string, manifest Config, destination InfraResolution) (string, error) {
	name := destination.Program.Name
	if name == "." || name == ".." || !recordNamespacePattern.MatchString(name) {
		return "", fmt.Errorf("infrastructure program name must be a safe record namespace")
	}
	directory := manifest.Workspace.Deployment.Records
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, directory)
	}
	directory, err := filepath.Abs(filepath.Join(directory, name, destination.Stack))
	if err != nil {
		return "", err
	}
	if err := validateDeploymentRecordDirectory(root, directory, manifest); err != nil {
		return "", err
	}
	return directory, nil
}

func validateDeploymentRecordDirectory(root, path string, manifest Config) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	recordAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for _, project := range manifest.Workspace.Projects {
		projectPath := filepath.Join(rootAbs, project.Path)
		rel, err := filepath.Rel(projectPath, recordAbs)
		if err != nil {
			return err
		}
		if rel == "." || !escapesRoot(rel) {
			return fmt.Errorf("deployment records cannot be inside project %s", project.Name)
		}
	}
	rel, err := filepath.Rel(rootAbs, recordAbs)
	if err != nil {
		return err
	}
	if !escapesRoot(rel) {
		return rejectSymlinkComponents(rootAbs, recordAbs)
	}
	info, err := os.Lstat(recordAbs)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("deployment record path is a symlink")
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readReleaseOutputs(ctx context.Context, runner infraCommandRunner, d InfraResolution, names []string, env map[string]string) (map[string]string, error) {
	scopedEnv := map[string]string{}
	for k, v := range env {
		scopedEnv[k] = v
	}
	scopedEnv["PULUMI_BACKEND_URL"] = d.BackendURL
	env = scopedEnv
	var output bytes.Buffer
	private := infraCommandRunner{stdout: &output, stderr: io.Discard, private: true}
	if err := private.run(ctx, d.Directory, env, "pulumi", "stack", "output", "--stack", d.Stack, "--json", "--non-interactive"); err != nil {
		return nil, fmt.Errorf("read release identities failed")
	}
	values := map[string]any{}
	if err := json.Unmarshal(output.Bytes(), &values); err != nil {
		return nil, fmt.Errorf("invalid release output JSON")
	}
	result := map[string]string{}
	for _, name := range names {
		value, ok := values[name].(string)
		if !ok || value == "" || value == "[secret]" {
			return nil, fmt.Errorf("release output %s must be a non-secret string", name)
		}
		result[name] = value
	}
	return result, nil
}
