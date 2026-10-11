package core

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Infrastructure declares named state backends and non-secret environment settings.
type Infrastructure struct {
	Backend      string                      `yaml:"backend"`
	Backends     map[string]InfraBackend     `yaml:"backends"`
	Environments map[string]InfraEnvironment `yaml:"environments"`
	Bindings     map[string]InfraBinding     `yaml:"bindings,omitempty"`
}

// InfraBackend identifies an existing DIY state store. Backend changes never migrate state.
type InfraBackend struct {
	URL        string `yaml:"url"`
	AllowLocal bool   `yaml:"allow_local,omitempty"`
}

// InfraEnvironment holds explicit, non-secret configuration for an environment.
type InfraEnvironment struct {
	Backend string            `yaml:"backend,omitempty"`
	Config  map[string]string `yaml:"config,omitempty"`
}

// InfraDeclaration distinguishes executable programs from imported components.
type InfraDeclaration struct {
	Role    string   `yaml:"role"`
	Scope   string   `yaml:"scope,omitempty"`
	Targets []string `yaml:"targets,omitempty"`
}

// InfraBinding connects an app's artifact to its owning program.
type InfraBinding struct {
	Program  string `yaml:"program"`
	Artifact string `yaml:"artifact"`
}

// TaskReference names a project-owned task and its stack output inputs.
type TaskReference struct {
	Project        string            `yaml:"project"`
	Task           string            `yaml:"task"`
	Outputs        map[string]string `yaml:"outputs,omitempty"`
	administration bool
}

// Deployment declares ordering and client-owned recovery contracts.
type Deployment struct {
	Prerequisites []string                `yaml:"prerequisites,omitempty"`
	Migrations    []TaskReference         `yaml:"migrations,omitempty"`
	Applications  []DeploymentApplication `yaml:"applications"`
	Recovery      DeploymentRecovery      `yaml:"recovery"`
	Records       string                  `yaml:"records"`
	RecordOutputs []string                `yaml:"record_outputs,omitempty"`
}

// DeploymentApplication declares exactly one deployment owner.
type DeploymentApplication struct {
	Project   string            `yaml:"project"`
	ManagedBy string            `yaml:"managed_by"`
	Deploy    string            `yaml:"deploy,omitempty"`
	Health    string            `yaml:"health"`
	Rollback  string            `yaml:"rollback"`
	Outputs   map[string]string `yaml:"outputs,omitempty"`
}

// DeploymentRecovery declares compensation without guessing database down migrations.
type DeploymentRecovery struct {
	Provision   *TaskReference `yaml:"provision,omitempty"`
	Migration   *TaskReference `yaml:"migration,omitempty"`
	FirstDeploy *TaskReference `yaml:"first_deploy"`
	Database    *TaskReference `yaml:"database,omitempty"`
}

// Private helpers ------------------------------------------------------------

func validateInfrastructure(manifest Config) error {
	programs := map[string]int{}
	for _, project := range manifest.Workspace.Projects {
		if project.Infra == nil {
			continue
		}
		if !strings.HasPrefix(filepath.ToSlash(filepath.Clean(project.Path)), "infra/") {
			return fmt.Errorf("infra project %s must be under infra/", project.Name)
		}
		if err := validateInfraDeclaration(project.Infra); err != nil {
			return fmt.Errorf("project %s: %w", project.Name, err)
		}
		if project.Infra.Role == "program" {
			programs[project.Infra.Scope]++
		}
	}
	if programs["environment"] > 1 || programs["shared"] > 1 {
		return fmt.Errorf("only one program may own each infrastructure scope")
	}
	for _, template := range manifest.DeclaredTemplates() {
		if err := validateInfraDeclaration(template.Infra); err != nil {
			return fmt.Errorf("template %s: %w", template.Name, err)
		}
		if template.Infra != nil {
			if template.Kind == "infra" && template.Infra.Role != "program" {
				return fmt.Errorf("infra template %s must declare program role", template.Name)
			}
			if template.Kind == "infra-component" && template.Infra.Role != "component" {
				return fmt.Errorf("component template %s must declare component role", template.Name)
			}
		}
		if (template.Kind == "infra" || template.Kind == "infra-component") && template.Infra == nil {
			return fmt.Errorf("template %s requires infra metadata", template.Name)
		}
	}
	if manifest.Workspace.Infra != nil {
		infra := manifest.Workspace.Infra
		if _, ok := infra.Backends[infra.Backend]; !ok {
			return fmt.Errorf("infrastructure default backend %q is not declared", infra.Backend)
		}
		for name, backend := range infra.Backends {
			if err := validateBackend(backend); err != nil {
				return fmt.Errorf("backend %s: %w", name, err)
			}
		}
		for app, binding := range infra.Bindings {
			var owner, application Project
			for _, p := range manifest.Workspace.Projects {
				if p.Name == app {
					application = p
				}
				if p.Name == binding.Program {
					owner = p
				}
			}
			if application.Name == "" || application.Infra != nil || owner.Infra == nil || owner.Infra.Role != "program" {
				return fmt.Errorf("invalid infra binding for %s", app)
			}
			if binding.Artifact == "" || filepath.IsAbs(binding.Artifact) || escapesRoot(binding.Artifact) {
				return fmt.Errorf("binding %s artifact must be relative to app", app)
			}
		}
		for name, env := range infra.Environments {
			if name != "dev" && name != "stage" && name != "prod" && name != "preview" && name != "shared" {
				return fmt.Errorf("unsupported environment %q", name)
			}
			if env.Backend != "" {
				if _, ok := infra.Backends[env.Backend]; !ok {
					return fmt.Errorf("environment %s backend not declared", name)
				}
			}
		}
	}
	d := manifest.Workspace.Deployment
	if d == nil {
		return nil
	}
	if manifest.Workspace.Infra == nil || programs["environment"] != 1 {
		return fmt.Errorf("deployment requires one environment program and configured infrastructure")
	}
	if d.Records == "" {
		return fmt.Errorf("deployment.records must identify a persistent recovery directory")
	}
	if len(d.Migrations) > 0 && len(d.Prerequisites) == 0 {
		return fmt.Errorf("migrations require explicit prerequisite targets")
	}
	projects := map[string]Project{}
	for _, p := range manifest.Workspace.Projects {
		projects[p.Name] = p
	}
	check := func(ref *TaskReference) error {
		if ref == nil {
			return nil
		}
		if _, ok := projects[ref.Project]; !ok || ref.Task == "" {
			return fmt.Errorf("invalid deployment task %s/%s", ref.Project, ref.Task)
		}
		return nil
	}
	seen := map[string]bool{}
	for _, m := range d.Migrations {
		if err := check(&m); err != nil {
			return err
		}
		key := m.Project + ":" + m.Task
		if seen[key] {
			return fmt.Errorf("duplicate migration owner %s", key)
		}
		seen[key] = true
	}
	for _, a := range d.Applications {
		if _, ok := projects[a.Project]; !ok {
			return fmt.Errorf("unknown deployment app %s", a.Project)
		}
		if seen["app:"+a.Project] {
			return fmt.Errorf("duplicate deployment app %s", a.Project)
		}
		seen["app:"+a.Project] = true
		if a.ManagedBy != "infra" && a.ManagedBy != "task" {
			return fmt.Errorf("app %s managed_by must be infra or task", a.Project)
		}
		if a.ManagedBy == "infra" && a.Deploy != "" {
			return fmt.Errorf("infra-managed app %s cannot declare deploy task", a.Project)
		}
		if a.ManagedBy == "task" && a.Deploy == "" {
			return fmt.Errorf("task-managed app %s requires deploy task", a.Project)
		}
		if a.Health == "" || a.Rollback == "" {
			return fmt.Errorf("app %s requires health and rollback tasks", a.Project)
		}
	}
	if d.Recovery.FirstDeploy == nil {
		return fmt.Errorf("deployment requires first_deploy recovery task")
	}
	for _, r := range []*TaskReference{d.Recovery.FirstDeploy, d.Recovery.Provision, d.Recovery.Migration, d.Recovery.Database} {
		if err := check(r); err != nil {
			return err
		}
	}
	return nil
}
func validateInfraDeclaration(d *InfraDeclaration) error {
	if d == nil {
		return nil
	}
	if d.Role != "program" && d.Role != "component" {
		return fmt.Errorf("infra.role must be program or component")
	}
	if d.Role == "program" && d.Scope != "environment" && d.Scope != "shared" {
		return fmt.Errorf("program infra.scope must be environment or shared")
	}
	if d.Role == "component" && d.Scope != "" {
		return fmt.Errorf("component cannot own a stack scope")
	}
	return nil
}
func validateBackend(b InfraBackend) error {
	u, err := url.Parse(b.URL)
	if err != nil {
		return err
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("backend URL must not contain credentials, query or fragment")
	}
	switch u.Scheme {
	case "gs", "s3", "azblob":
		if u.Host == "" {
			return fmt.Errorf("remote backend requires a container")
		}
	case "file":
		if !b.AllowLocal || u.Host != "" || !filepath.IsAbs(u.Path) {
			return fmt.Errorf("local state requires allow_local and absolute file URL")
		}
	default:
		return fmt.Errorf("backend must use gs, s3, azblob, or explicit local file")
	}
	return nil
}
