package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// MigrationGroup is one CI job. Its application tasks run in the declared order.
type MigrationGroup struct {
	Name     string   `json:"group"`
	Projects []string `json:"projects"`
}

// Public API -----------------------------------------------------------------

// PlanMigrations discovers application-owned migrate tasks without running them.
// Explicit groups serialize applications that share migration history.
func PlanMigrations(root string, manifest Config) ([]MigrationGroup, error) {
	deployment := manifest.Workspace.Deployment
	if deployment == nil {
		return nil, fmt.Errorf("workspace.deployment is not configured")
	}
	migrating := map[string]bool{}
	for _, app := range deployment.Applications {
		for _, project := range manifest.Workspace.Projects {
			if project.Name != app.Project {
				continue
			}
			directory, err := TemplateDirectory(root, project.Path)
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(filepath.Join(directory, "mise.toml"))
			if err != nil {
				return nil, fmt.Errorf("inspect %s migration task: %w", app.Project, err)
			}
			configuration, err := ExtractMiseConfig(project.Name, data)
			if err != nil {
				return nil, err
			}
			_, migrating[project.Name] = configuration.Tasks["migrate"]
		}
	}
	groups := []MigrationGroup{}
	assigned := map[string]bool{}
	names := map[string]bool{}
	var declared []string
	for name := range deployment.MigrationGroups {
		declared = append(declared, name)
	}
	sort.Strings(declared)
	for _, name := range declared {
		projects := deployment.MigrationGroups[name]
		for _, project := range projects {
			if !migrating[project] {
				return nil, fmt.Errorf("migration group %s includes %s, which has no application migrate task", name, project)
			}
			if assigned[project] {
				return nil, fmt.Errorf("application %s appears in multiple migration groups", project)
			}
			assigned[project] = true
		}
		names[name] = true
		groups = append(groups, MigrationGroup{Name: name, Projects: projects})
	}
	for _, app := range deployment.Applications {
		if !migrating[app.Project] || assigned[app.Project] {
			continue
		}
		if names[app.Project] {
			return nil, fmt.Errorf("migration group %s conflicts with an independent application job; rename the group", app.Project)
		}
		groups = append(groups, MigrationGroup{Name: app.Project, Projects: []string{app.Project}})
	}
	return groups, nil
}

// RunMigrations runs optional application tasks concurrently across independent groups.
// Tasks within an explicitly shared group run in order. All workers stop before return.
func RunMigrations(ctx context.Context, root string, options InfraOptions, group string, stdout, stderr io.Writer) (resultErr error) {
	manifest, err := LoadManifest(root + "/" + ManifestFilename)
	if err != nil {
		return err
	}
	groups, err := PlanMigrations(root, manifest)
	if err != nil {
		return err
	}
	stdout, stderr = synchronizedDeploymentWriters(stdout, stderr)
	runner := infraCommandRunner{stdout: stdout, stderr: stderr}
	environment := map[string]string{"PM_BRANCH": options.Branch}
	if manifest.Workspace.Infra != nil {
		destination, err := ResolveInfrastructure(root, options)
		if err != nil {
			return err
		}
		environment["PM_STACK"], environment["PULUMI_BACKEND_URL"] = destination.Stack, destination.BackendURL
		environment["PM_INFRA_BACKEND"] = options.Backend
		release, err := acquireInfrastructureOwnership(ctx, root, destination)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, release()) }()

	}
	var workers sync.WaitGroup
	var failures []error
	var mutex sync.Mutex
	found := group == ""
	for _, selected := range groups {
		if group != "" && selected.Name != group {
			continue
		}
		found = true
		workers.Add(1)
		go func(selected MigrationGroup) {
			defer workers.Done()
			for _, project := range selected.Projects {
				err := runDeploymentTask(ctx, root, manifest, TaskReference{Project: project, Task: "migrate"}, environment, runner, DeployOptions{InfraOptions: options})
				if err != nil {
					mutex.Lock()
					failures = append(failures, err)
					mutex.Unlock()
					return
				}
			}
		}(selected)
	}
	workers.Wait()
	if !found {
		return fmt.Errorf("migration group %q is not configured", group)
	}
	return errors.Join(failures...)
}

// Private helpers ------------------------------------------------------------

type deploymentWriter struct {
	destination io.Writer
	mutex       *sync.Mutex
}

func (w deploymentWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.destination.Write(data)
}

func synchronizedDeploymentWriters(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	mutex := &sync.Mutex{}
	if stdout != nil {
		stdout = deploymentWriter{destination: stdout, mutex: mutex}
	}
	if stderr != nil {
		stderr = deploymentWriter{destination: stderr, mutex: mutex}
	}
	return stdout, stderr
}
