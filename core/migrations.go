package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// MigrationGroup is one CI job. Its application tasks run in the declared order.
type MigrationGroup struct {
	Name     string   `json:"group"`
	Projects []string `json:"projects"`
}

// MigrationMatrix supplies the include rows consumed by a CI strategy.
type MigrationMatrix struct {
	Include []MigrationGroup `json:"include"`
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
	if len(groups) > 0 && len(manifest.Workspace.Infra.Prerequisites) == 0 {
		return nil, fmt.Errorf("applications with migrate tasks require workspace.infra.prerequisites before deployment")
	}
	return groups, nil
}
