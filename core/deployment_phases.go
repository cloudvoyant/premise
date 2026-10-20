package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cloudvoyant/premise/core/providers"
)

// Public API -----------------------------------------------------------------

// RunDeploymentPhase runs one phase of a deployment shared across CI jobs.
// Every phase must use the run ID that acquired the environment lock at start.
func RunDeploymentPhase(ctx context.Context, root, phase string, options DeployOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	switch phase {
	case "start", "provision", "migrate", "apply", "complete", "recover", "unlock":
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
	if phase == "provision" || phase == "apply" || phase == "complete" {
		if err := EnsureInfrastructureEnvironment(root, runningInCI()); err != nil {
			return err
		}
	}
	runner := infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}
	environment := deploymentTaskEnvironment(run)
	// A phase can run in a fresh checkout. Stack settings are local files, so
	// restore declared environment values before running the client program.
	if phase != "provision" && run.Phase != "started" && run.Phase != "init" && run.Phase != "completed" {
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
	case "provision":
		if run.Phase != "started" {
			return fmt.Errorf("provision requires a newly started deployment, got phase %s", run.Phase)
		}
		if err := setPhase("init"); err != nil {
			return err
		}
		if err := executeInfra(ctx, runner, destination, "init", true, environment); err != nil {
			return err
		}
		if err := setPhase("provision"); err != nil {
			return err
		}
		if len(manifest.Workspace.Infra.Prerequisites) > 0 {
			targets, err := resolvePrerequisiteTargets(ctx, runner, destination, manifest.Workspace.Infra.Prerequisites, environment)
			if err != nil {
				return err
			}
			if err := apply(targets); err != nil {
				return err
			}
		}
		return setPhase("provisioned")
	case "migrate":
		if run.Phase != "provisioned" {
			return fmt.Errorf("migrations require completed provisioning, got phase %s", run.Phase)
		}
		return runMigrationGroup(ctx, store, run, options.Group, task)
	case "apply":
		if run.Phase != "provisioned" {
			return fmt.Errorf("full update requires completed provisioning, got phase %s", run.Phase)
		}
		if err := requireCompletedMigrations(ctx, store, run); err != nil {
			return err
		}
		if err := setPhase("apply"); err != nil {
			return err
		}
		if err := apply(nil); err != nil {
			return err
		}
		return setPhase("applied")
	case "complete":
		if run.Phase != "applied" {
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

// Private helpers ------------------------------------------------------------

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

func requireCompletedMigrations(ctx context.Context, store providers.ObjectStore, run deploymentRun) error {
	for _, group := range run.Groups {
		object, err := store.Read(ctx, migrationRecordKey(run, group.Name))
		if err != nil {
			return fmt.Errorf("migration group %s has not completed: %w", group.Name, err)
		}
		var outcome migrationOutcome
		if err := json.Unmarshal(object.Data, &outcome); err != nil || outcome.Status != "succeeded" {
			return fmt.Errorf("migration group %s must succeed before the full infrastructure update", group.Name)
		}
	}
	return nil
}

func recoverDeploymentRun(ctx context.Context, store providers.ObjectStore, run deploymentRun, lockVersion string, workersStopped bool, manifest Config, destination InfraResolution, runner infraCommandRunner, environment map[string]string, task func(TaskReference) error) error {
	phase := run.Phase
	if phase == "started" {
		phase = "init"
	}
	if phase == "provisioned" {
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
