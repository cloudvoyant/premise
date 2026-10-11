package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestPreviewStackIsolation(t *testing.T) {
	a, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature/a"})
	again, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature/a"})
	b, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature-a"})
	if a != again || a == b {
		t.Fatal("preview identity is unstable or collides")
	}
}
func TestBackendValidation(t *testing.T) {
	for _, url := range []string{"https://app.pulumi.com", "gs://", "gs://user:password@bucket", "file:///tmp/state"} {
		if validateBackend(InfraBackend{URL: url}) == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	if err := validateBackend(InfraBackend{URL: "gs://premise/state"}); err != nil {
		t.Fatal(err)
	}
}
func TestResolveSameEnvironmentStack(t *testing.T) {
	root := t.TempDir()
	manifest := infraTestManifest()
	if err := os.MkdirAll(filepath.Join(root, "infra/environment"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "environment", "db"} {
		d, err := ResolveInfrastructure(root, InfraOptions{Environment: "dev", Project: name})
		if err != nil {
			t.Fatal(err)
		}
		if d.Stack != "dev" || d.Program.Name != "environment" {
			t.Fatal(d)
		}
	}
}
func TestFullApplyDoesNotRetainPrerequisiteFilter(t *testing.T) {
	r := &recordingInfraRunner{}
	d := InfraResolution{Directory: "infra", Stack: "dev", BackendURL: "gs://state"}
	if err := executeInfra(context.Background(), r, d, "apply", true, nil); err != nil {
		t.Fatal(err)
	}
	last := r.commands[len(r.commands)-1]
	if strings.Contains(strings.Join(last, " "), "--target") {
		t.Fatal(last)
	}
}
func TestRecoveryDoesNotRunMigrations(t *testing.T) {
	config := &Deployment{Migrations: []TaskReference{{Project: "web", Task: "migrate"}}, Applications: []DeploymentApplication{{Project: "web", Rollback: "rollback"}}, Recovery: DeploymentRecovery{FirstDeploy: &TaskReference{Project: "web", Task: "cleanup"}}}
	var tasks []string
	r := &recordingInfraRunner{}
	err := recoverDeployment(context.Background(), r, InfraResolution{Stack: "preview-test"}, config, &ReleaseRecord{Release: "A"}, "health", func(ref TaskReference) error { tasks = append(tasks, ref.Task); return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tasks, []string{"rollback"}) {
		t.Fatal(tasks)
	}
}
func TestRecoveryFailureReported(t *testing.T) {
	config := &Deployment{Applications: []DeploymentApplication{{Project: "web", Rollback: "rollback"}}}
	err := recoverDeployment(context.Background(), &recordingInfraRunner{}, InfraResolution{}, config, &ReleaseRecord{}, "apply", func(TaskReference) error { return errors.New("recovery failed") }, nil)
	if err == nil {
		t.Fatal("rollback failure lost")
	}
}
func TestPrerequisiteCoverageRejectsApplicationDependency(t *testing.T) {
	resources := map[string]infraResourcePlan{"urn:db": {Goal: &infraPlanGoal{Custom: true, Dependencies: []string{"urn:app"}}}, "urn:app": {Goal: &infraPlanGoal{Custom: true}}}
	if _, err := validatePrerequisiteCoverage(resources, []string{"urn:db"}, "dev", "example"); err == nil {
		t.Fatal("accepted missing prerequisite")
	}
	if _, err := validatePrerequisiteCoverage(resources, []string{"urn:missing"}, "dev", "example"); err == nil {
		t.Fatal("accepted unmatched target")
	}
}
func TestPrerequisiteCoverageIncludesProvider(t *testing.T) {
	resources := map[string]infraResourcePlan{"urn:db": {Goal: &infraPlanGoal{Custom: true, Provider: "urn:provider::id"}}, "urn:provider": {Goal: &infraPlanGoal{Custom: true, Type: "pulumi:providers:gcp"}}}
	targets, err := validatePrerequisiteCoverage(resources, []string{"urn:db"}, "dev", "example")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targets, []string{"urn:db", "urn:provider"}) {
		t.Fatal(targets)
	}
}

// Private helpers ------------------------------------------------------------

func infraTestManifest() Config {
	manifest := NewManifest("example")
	manifest.Workspace.Infra = &Infrastructure{Backend: "remote", Backends: map[string]InfraBackend{"remote": {URL: "gs://test"}}, Environments: map[string]InfraEnvironment{"dev": {}}}
	manifest.Workspace.Projects = []Project{{Name: "environment", Template: "registry/environment", Path: "infra/environment", Infra: &InfraDeclaration{Role: "program", Scope: "environment"}}, {Name: "db", Path: "infra/components/db", Infra: &InfraDeclaration{Role: "component", Targets: []string{"urn:pulumi:dev::example::type::db"}}}}
	return manifest
}

type recordingInfraRunner struct{ commands [][]string }

func (r *recordingInfraRunner) run(_ context.Context, _ string, _ map[string]string, name string, args ...string) error {
	r.commands = append(r.commands, append([]string{name}, args...))
	return nil
}
