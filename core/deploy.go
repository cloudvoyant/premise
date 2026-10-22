package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cloudvoyant/premise/core/extensions"
	"github.com/cloudvoyant/premise/core/providers"
)

// Public API -----------------------------------------------------------------

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

// ReleaseRecord remembers the artifacts that passed deployment and health checks.
// Premise saves it as last-success.json so another runner can restore that release.
// It contains recovery references, never database credentials or application secrets.
type ReleaseRecord struct {
	RunID     string            `json:"run_id,omitempty"`
	Release   string            `json:"release"`
	Artifacts map[string]string `json:"artifacts"`
	Program   string            `json:"program"`
	Stack     string            `json:"stack"`
	Backend   string            `json:"backend"`
	Completed time.Time         `json:"completed"`
	Outputs   map[string]string `json:"outputs,omitempty"`
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
	output := &secretRedactor{destination: runner.stdout, values: sensitive}
	diagnostics := &secretRedactor{destination: runner.stderr, values: sensitive}
	if project.Secrets != nil && base["PM_FAILED_PHASE"] == "" {
		directory, config, profile, err := resolveProjectSecrets(root, project, options.Environment, options.Branch)
		if err != nil {
			return err
		}
		fnox := extensions.Fnox{Environment: extensions.PulumiEnvironment(environment), Stdin: runner.stdin, Stdout: output, Stderr: diagnostics}
		runErr := fnox.Exec(ctx, directory, config, profile, "mise", "run", ref.Task, "--", options.Environment)
		return errors.Join(runErr, output.Flush(), diagnostics.Flush())
	}
	command := exec.CommandContext(ctx, "mise", "run", ref.Task, "--", options.Environment)
	command.Dir = directory
	command.Env = extensions.PulumiEnvironment(environment)
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

// runDeploymentPhase runs one phase of a deployment shared across CI jobs.
// Every phase must use the run ID that acquired the environment lock at start.
func runDeploymentPhase(ctx context.Context, root, phase string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	switch phase {
	case "start", "prepare", "migrate", "update", "complete", "recover", "unlock":
	default:
		return fmt.Errorf("unknown deployment phase %q", phase)
	}
	if err := ValidateProjectName(options.RunID); err != nil {
		return fmt.Errorf("deployment phases require a safe --run-id: %w", err)
	}
	manifest, err := LoadManifest(root + "/" + ManifestFilename)
	if err != nil {
		return err
	}
	if manifest.Workspace.Deployment == nil || options.Shared {
		return fmt.Errorf("deployment phases require workspace.deployment and an environment stack")
	}
	destination, err := ResolveInfrastructure(root, options.InfraOptions)
	if err != nil {
		return err
	}
	store, err := deploymentRecordStore(root, manifest, destination)
	if err != nil {
		return err
	}
	if phase == "start" {
		return startDeploymentRun(ctx, root, manifest, destination, store, options)
	}
	lockVersion, err := assertDeploymentOwner(ctx, store, options.RunID)
	if err != nil {
		return err
	}
	if phase == "unlock" {
		return store.Delete(ctx, "lock.json", lockVersion)
	}
	run, version, err := readDeploymentRun(ctx, store, options.RunID)
	if err != nil {
		return err
	}
	if run.Project != destination.ProjectName || run.Stack != destination.Stack || run.Backend != destination.BackendURL {
		return fmt.Errorf("deployment run %s belongs to a different Pulumi project, stack, or backend", run.ID)
	}
	if phase == "prepare" || phase == "update" || phase == "complete" {
		if err := EnsureInfrastructureEnvironment(root, runningInCI()); err != nil {
			return err
		}
	}
	runner := infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}
	environment := deploymentTaskEnvironment(run)
	// A phase can run in a fresh checkout. Stack settings are local files, so
	// restore declared environment values before running the client program.
	// Migration workers only query saved state. Do not rewrite stack settings
	// from parallel workers that may share the same checkout.
	if phase != "migrate" && phase != "prepare" && run.Phase != "started" && run.Phase != "init" && run.Phase != "completed" {
		if err := executeInfra(ctx, runner, destination, "select", false, environment); err != nil {
			return err
		}
	}
	task := func(reference TaskReference) error {
		return runDeploymentTask(ctx, root, manifest, reference, environment, runner, run.Options)
	}
	setPhase := func(name string) error {
		run.Phase = name
		if err := writeDeploymentRecord(ctx, store, "runs/"+run.ID+".json", run, version); err != nil {
			return err
		}
		_, updated, err := readDeploymentRun(ctx, store, run.ID)
		version = updated
		return err
	}
	apply := func(targets []string) error {
		selected := destination
		selected.Targets = targets
		if err := executeInfra(ctx, runner, selected, "preview", false, environment); err != nil {
			return err
		}
		return executeInfra(ctx, runner, selected, "apply", true, environment)
	}
	switch phase {
	case "prepare":
		if err := setPhase("init"); err != nil {
			return err
		}
		if err := prepareInfrastructure(ctx, destination, runner, environment); err != nil {
			return err
		}
		return setPhase("prepared")
	case "migrate":
		if run.Phase != "prepared" && run.Phase != "updated" {
			return fmt.Errorf("migrations require infrastructure setup, got phase %s", run.Phase)
		}
		return runMigrationGroup(ctx, store, run, options.Group, task)
	case "update":
		if run.Phase != "prepared" {
			return fmt.Errorf("full update requires completed infrastructure setup, got phase %s", run.Phase)
		}
		if err := setPhase("apply"); err != nil {
			return err
		}
		if err := apply(nil); err != nil {
			return err
		}
		return setPhase("updated")
	case "complete":
		if run.Phase != "started" && run.Phase != "updated" {
			return fmt.Errorf("completion requires a successful full update, got phase %s", run.Phase)
		}
		if err := setPhase("deploy"); err != nil {
			return err
		}
		for _, app := range manifest.Workspace.Deployment.Applications {
			if app.Deploy != "" {
				if err := task(TaskReference{Project: app.Project, Task: app.Deploy}); err != nil {
					return err
				}
			}
		}
		if err := setPhase("health"); err != nil {
			return err
		}
		for _, app := range manifest.Workspace.Deployment.Applications {
			if err := task(TaskReference{Project: app.Project, Task: app.Health}); err != nil {
				return err
			}
		}
		if err := setPhase("record"); err != nil {
			return err
		}
		outputs, err := readReleaseOutputs(ctx, runner, destination, manifest.Workspace.Deployment.RecordOutputs, environment)
		if err != nil {
			return err
		}
		release := ReleaseRecord{
			RunID:     run.ID,
			Release:   run.Options.Release,
			Artifacts: run.Options.Artifacts,
			Program:   run.Project,
			Stack:     run.Stack,
			Backend:   run.Backend,
			Completed: time.Now().UTC(),
			Outputs:   outputs,
		}
		if err := writeDeploymentRecord(ctx, store, "last-run.json", DeploymentResult{Release: release.Release, Succeeded: true}, ""); err != nil {
			return err
		}
		if err := writeDeploymentRecord(ctx, store, "last-success.json", release, ""); err != nil {
			return err
		}
		if err := setPhase("completed"); err != nil {
			return err
		}
		return store.Delete(ctx, "lock.json", lockVersion)
	case "recover":
		if run.Phase == "completed" {
			return store.Delete(ctx, "lock.json", lockVersion)
		}
		return recoverDeploymentRun(ctx, store, run, lockVersion, options.WorkersStopped, manifest, destination, runner, environment, task)
	default:
		return fmt.Errorf("unknown deployment phase %q", phase)
	}
}

func startDeploymentRun(ctx context.Context, root string, manifest Config, destination InfraResolution, store providers.ObjectStore, options DeployOptions) error {
	if options.Release == "" || len(options.Artifacts) == 0 {
		return fmt.Errorf("deployment start requires an immutable --release and pinned --artifact references")
	}
	for _, app := range manifest.Workspace.Deployment.Applications {
		if options.Artifacts[app.Project] == "" {
			return fmt.Errorf("application %s requires a pinned --artifact reference", app.Project)
		}
	}
	groups, err := PlanMigrations(root, manifest)
	if err != nil {
		return err
	}
	lock := deploymentLock{RunID: options.RunID, Created: time.Now().UTC()}
	if err := writeDeploymentRecord(ctx, store, "lock.json", lock, "*"); err != nil {
		return fmt.Errorf("cannot acquire environment deployment lock; inspect the active or interrupted run before unlocking: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			if version, err := assertDeploymentOwner(cleanup, store, options.RunID); err == nil {
				store.Delete(cleanup, "lock.json", version)
			}
		}
	}()
	previous, err := readSuccessfulRelease(ctx, store)
	if err != nil {
		return err
	}
	if previous != nil && (previous.Program != destination.ProjectName || previous.Stack != destination.Stack || previous.Backend != destination.BackendURL) {
		return fmt.Errorf("previous successful release belongs to a different Pulumi project, stack, or backend")
	}
	run := deploymentRun{
		ID:       options.RunID,
		Options:  options,
		Project:  destination.ProjectName,
		Stack:    destination.Stack,
		Backend:  destination.BackendURL,
		Phase:    "started",
		Previous: previous,
		Groups:   groups,
	}
	if err := writeDeploymentRecord(ctx, store, "runs/"+run.ID+".json", run, "*"); err != nil {
		return fmt.Errorf("save deployment run: %w", err)
	}
	succeeded = true
	return nil
}

func deploymentTaskEnvironment(run deploymentRun) map[string]string {
	environment := map[string]string{
		"PM_INFRA_BACKEND":   run.Options.Backend,
		"PM_ENV":             run.Options.Environment,
		"PM_BRANCH":          run.Options.Branch,
		"PM_STACK":           run.Stack,
		"PM_RELEASE":         run.Options.Release,
		"PM_RUN_ID":          run.ID,
		"PULUMI_BACKEND_URL": run.Backend,
	}
	artifacts, _ := json.Marshal(run.Options.Artifacts)
	environment["PM_ARTIFACTS"] = string(artifacts)
	if run.Previous != nil {
		environment["PM_PREVIOUS_RELEASE"] = run.Previous.Release
		previousArtifacts, _ := json.Marshal(run.Previous.Artifacts)
		environment["PM_PREVIOUS_ARTIFACTS"] = string(previousArtifacts)
		previousOutputs, _ := json.Marshal(run.Previous.Outputs)
		environment["PM_PREVIOUS_OUTPUTS"] = string(previousOutputs)
	}
	return environment
}

func migrationRecordKey(run deploymentRun, group string) string {
	return "runs/" + run.ID + "/migrations/" + group + ".json"
}

func runMigrationGroup(ctx context.Context, store providers.ObjectStore, run deploymentRun, name string, task func(TaskReference) error) error {
	for _, group := range run.Groups {
		if group.Name != name {
			continue
		}
		key := migrationRecordKey(run, name)
		if err := writeDeploymentRecord(ctx, store, key, migrationOutcome{Status: "running"}, "*"); err != nil {
			return fmt.Errorf("migration group %s already ran or cannot be claimed: %w", name, err)
		}
		for _, project := range group.Projects {
			if err := task(TaskReference{Project: project, Task: "migrate"}); err != nil {
				return errors.Join(err, writeDeploymentRecord(context.WithoutCancel(ctx), store, key, migrationOutcome{Status: "failed"}, ""))
			}
		}
		return writeDeploymentRecord(ctx, store, key, migrationOutcome{Status: "succeeded"}, "")
	}
	return fmt.Errorf("deployment %s has no migration group %q", run.ID, name)
}

func recoverDeploymentRun(ctx context.Context, store providers.ObjectStore, run deploymentRun, lockVersion string, workersStopped bool, manifest Config, destination InfraResolution, runner infraCommandRunner, environment map[string]string, task func(TaskReference) error) error {
	phase := run.Phase
	if phase == "started" {
		phase = "init"
	}
	if phase == "prepared" || phase == "updated" {
		phase = "migration"
		for _, group := range run.Groups {
			object, err := store.Read(ctx, migrationRecordKey(run, group.Name))
			if errors.Is(err, providers.ErrObjectNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			var outcome migrationOutcome
			if err := json.Unmarshal(object.Data, &outcome); err != nil {
				return fmt.Errorf("read migration group %s outcome: %w", group.Name, err)
			}
			if outcome.Status == "running" {
				if !workersStopped {
					return fmt.Errorf("migration group %s is still marked running; stop every worker, then recover with --workers-stopped", group.Name)
				}
				if err := writeDeploymentRecord(ctx, store, migrationRecordKey(run, group.Name), migrationOutcome{Status: "interrupted"}, object.Version); err != nil {
					return err
				}
			}
		}
	}
	environment["PM_FAILED_PHASE"] = phase
	recoveryErr := recoverDeployment(ctx, runner, destination, manifest.Workspace.Deployment, run.Previous, phase, task, environment)
	if recoveryErr == nil {
		recoveryErr = restorePreviousReleaseRecord(ctx, store, run)
	}
	result := DeploymentResult{
		Release:           run.Options.Release,
		Phase:             phase,
		RecoveryAttempted: true,
		RecoverySucceeded: recoveryErr == nil,
	}
	recordErr := writeDeploymentRecord(ctx, store, "last-run.json", result, "")
	if recoveryErr != nil || recordErr != nil {
		return errors.Join(recoveryErr, recordErr)
	}
	return store.Delete(ctx, "lock.json", lockVersion)
}

func restorePreviousReleaseRecord(ctx context.Context, store providers.ObjectStore, run deploymentRun) error {
	object, err := store.Read(ctx, "last-success.json")
	if errors.Is(err, providers.ErrObjectNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var current ReleaseRecord
	if err := json.Unmarshal(object.Data, &current); err != nil {
		return err
	}
	if current.RunID != run.ID {
		return nil
	}
	if run.Previous == nil {
		return store.Delete(ctx, "last-success.json", object.Version)
	}
	return writeDeploymentRecord(ctx, store, "last-success.json", run.Previous, object.Version)
}

var recordNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type deploymentRun struct {
	ID       string           `json:"id"`
	Options  DeployOptions    `json:"options"`
	Project  string           `json:"project"`
	Stack    string           `json:"stack"`
	Backend  string           `json:"backend"`
	Phase    string           `json:"phase"`
	Previous *ReleaseRecord   `json:"previous,omitempty"`
	Groups   []MigrationGroup `json:"migrations"`
}

type deploymentLock struct {
	RunID   string    `json:"run_id"`
	Created time.Time `json:"created"`
}

type migrationOutcome struct {
	Status string `json:"status"`
}

func deploymentRecordStore(root string, manifest Config, destination InfraResolution) (providers.ObjectStore, error) {
	base := manifest.Workspace.Deployment.Records
	if base == "" {
		base = strings.TrimRight(destination.BackendURL, "/") + "/.premise-releases"
	}
	if !strings.Contains(base, "://") {
		directory, err := deploymentRecordDirectory(root, manifest, destination)
		if err != nil {
			return providers.ObjectStore{}, err
		}
		return providers.ObjectStore{URL: (&url.URL{Scheme: "file", Path: filepath.ToSlash(directory)}).String()}, nil
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return providers.ObjectStore{}, fmt.Errorf("deployment.records must be a credential-free storage prefix")
	}
	if parsed.Scheme != "s3" && parsed.Scheme != "gs" && parsed.Scheme != "file" {
		return providers.ObjectStore{}, fmt.Errorf("deployment.records supports s3://, gs://, and explicit local file:// storage")
	}
	if parsed.Scheme == "file" {
		if parsed.Host != "" || !filepath.IsAbs(parsed.Path) {
			return providers.ObjectStore{}, fmt.Errorf("local deployment records require an absolute file URL")
		}
		if runningInCI() && !strings.HasPrefix(destination.BackendURL, "file://") {
			return providers.ObjectStore{}, fmt.Errorf("CI deployment records require shared s3:// or gs:// storage, not runner-local files")
		}
	} else if parsed.Host == "" {
		return providers.ObjectStore{}, fmt.Errorf("deployment record prefix requires a bucket")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + destination.ProjectName + "/" + destination.Stack
	if parsed.Scheme == "file" {
		if err := validateDeploymentRecordDirectory(root, parsed.Path, manifest); err != nil {
			return providers.ObjectStore{}, err
		}
	}
	return providers.ObjectStore{URL: parsed.String()}, nil
}

// acquireInfrastructureOwnership prevents standalone commands from mutating a
// stack during an environment CI flow or application deployment.
func acquireInfrastructureOwnership(ctx context.Context, root string, destination InfraResolution) (func() error, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	if manifest.Workspace.Deployment == nil {
		return func() error { return nil }, nil
	}
	store, err := deploymentRecordStore(root, manifest, destination)
	if err != nil {
		return nil, err
	}
	// A client recovery task may call infra commands within its existing run.
	if id := os.Getenv("PM_RUN_ID"); id != "" {
		if _, err := assertDeploymentOwner(ctx, store, id); err != nil {
			return nil, err
		}
		return func() error { return nil }, nil
	}
	id := fmt.Sprintf("infra-%d", time.Now().UnixNano())
	if err := writeDeploymentRecord(ctx, store, "lock.json", deploymentLock{RunID: id, Created: time.Now().UTC()}, "*"); err != nil {
		return nil, fmt.Errorf("cannot mutate infrastructure while another run owns the environment lock: %w", err)
	}
	return func() error {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		version, err := assertDeploymentOwner(cleanup, store, id)
		if err != nil {
			return err
		}
		return store.Delete(cleanup, "lock.json", version)
	}, nil
}

func writeDeploymentRecord(ctx context.Context, store providers.ObjectStore, key string, value any, version string) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return store.Put(ctx, key, append(data, '\n'), version)
}

func readDeploymentRun(ctx context.Context, store providers.ObjectStore, id string) (deploymentRun, string, error) {
	var run deploymentRun
	object, err := store.Read(ctx, "runs/"+id+".json")
	if err != nil {
		return run, "", fmt.Errorf("read deployment run %s: %w", id, err)
	}
	if err := json.Unmarshal(object.Data, &run); err != nil || run.ID != id {
		return run, "", fmt.Errorf("deployment run %s contains invalid metadata", id)
	}
	return run, object.Version, nil
}

func assertDeploymentOwner(ctx context.Context, store providers.ObjectStore, id string) (string, error) {
	object, err := store.Read(ctx, "lock.json")
	if err != nil {
		return "", fmt.Errorf("deployment %s does not own the environment lock: %w", id, err)
	}
	var lock deploymentLock
	if err := json.Unmarshal(object.Data, &lock); err != nil || lock.RunID != id {
		return "", fmt.Errorf("environment is locked by another deployment; run %s cannot proceed", id)
	}
	return object.Version, nil
}

func readSuccessfulRelease(ctx context.Context, store providers.ObjectStore) (*ReleaseRecord, error) {
	object, err := store.Read(ctx, "last-success.json")
	if errors.Is(err, providers.ErrObjectNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var release ReleaseRecord
	if err := json.Unmarshal(object.Data, &release); err != nil {
		return nil, fmt.Errorf("decode last successful deployment: %w", err)
	}
	return &release, nil
}

func deploymentRecordDirectory(root string, manifest Config, destination InfraResolution) (string, error) {
	name := destination.ProjectName
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
	if len(names) == 0 {
		return nil, nil
	}
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
