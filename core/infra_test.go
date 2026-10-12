package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestInfraStackSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options InfraOptions
		want    string
		valid   bool
	}{
		{"development", InfraOptions{Environment: "dev"}, "dev", true},
		{"staging", InfraOptions{Environment: "stage"}, "stage", true},
		{"production", InfraOptions{Environment: "prod"}, "prod", true},
		{"shared", InfraOptions{Shared: true}, "shared", true},
		{"missing preview branch", InfraOptions{Environment: "preview"}, "", false},
		{"branch on production", InfraOptions{Environment: "prod", Branch: "feature/a"}, "", false},
		{"shared with environment", InfraOptions{Environment: "prod", Shared: true}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := InfraStack(tc.options)
			if (err == nil) != tc.valid || got != tc.want {
				t.Fatalf("got %q, %v; want %q, valid=%t", got, err, tc.want, tc.valid)
			}
		})
	}
	a, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature/a"})
	repeated, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature/a"})
	b, _ := InfraStack(InfraOptions{Environment: "preview", Branch: "feature-a"})
	if a != repeated || a == b {
		t.Fatal("preview identity is unstable or collides")
	}
}

func TestBackendValidation(t *testing.T) {
	for _, tc := range []struct {
		url               string
		allowLocal, valid bool
	}{
		{"gs://state", false, true}, {"s3://state", false, true}, {"azblob://state", false, true},
		{"https://app.pulumi.com", false, false}, {"gs://", false, false},
		{"gs://user:password@bucket", false, false}, {"gs://state?token=credential", false, false},
		{"file:///tmp/state", false, false}, {"file:///tmp/state", true, true},
	} {
		t.Run(tc.url, func(t *testing.T) {
			err := validateBackend(InfraBackend{URL: tc.url, AllowLocal: tc.allowLocal})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

func TestInfrastructureUsesPulumiEnvironmentWithoutProviderDeclarations(t *testing.T) {
	root := t.TempDir()
	manifest := infraTestManifest()
	manifest.Workspace.Infra = nil
	if err := os.MkdirAll(filepath.Join(root, "infra/environment"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULUMI_BACKEND_URL", "s3://client-owned-state")
	for _, environment := range []string{"dev", "stage", "prod"} {
		d, err := ResolveInfrastructure(root, InfraOptions{Environment: environment})
		if err != nil {
			t.Fatal(err)
		}
		if d.BackendURL != "s3://client-owned-state" || d.Stack != environment {
			t.Fatal(d)
		}
		if _, exists := d.Config["gcp:project"]; exists {
			t.Fatal("introduced provider config")
		}
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

func TestPrerequisitePatternsMatchPulumiTypesWithSlashes(t *testing.T) {
	branch := "urn:pulumi:preview-feature::environment::neon:index/branch:Branch::db-branch"
	database := "urn:pulumi:preview-feature::environment::neon:index/database:Database::db-database"
	app := "urn:pulumi:preview-feature::environment::vercel:index/deployment:Deployment::app-deployment"
	resources := map[string]infraResourcePlan{
		branch:   {Goal: &infraPlanGoal{Custom: true}},
		database: {Goal: &infraPlanGoal{Custom: true, Dependencies: []string{branch}}},
		app:      {Goal: &infraPlanGoal{Custom: true, Dependencies: []string{database}}},
	}
	targets, err := validatePrerequisiteCoverage(resources, []string{"urn:pulumi:{stack}::{project}::*::db-*"}, "preview-feature", "environment")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targets, []string{branch, database}) {
		t.Fatalf("targets = %q", targets)
	}
	if _, err := validatePrerequisiteCoverage(resources, []string{"["}, "preview-feature", "environment"); err == nil {
		t.Fatal("invalid wildcard accepted")
	}
}

// TestPulumiLocalLifecycle verifies real CLI state persistence without cloud resources.
func TestPulumiLocalLifecycle(t *testing.T) {
	binary := os.Getenv("PREMISE_TEST_PULUMI_BIN")
	if binary == "" {
		t.Skip("set PREMISE_TEST_PULUMI_BIN to run the real Pulumi local-state test")
	}
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PULUMI_HOME", t.TempDir())
	t.Setenv("PULUMI_CONFIG_PASSPHRASE", "test-only-passphrase")
	t.Setenv("PULUMI_SKIP_UPDATE_CHECK", "true")
	root := t.TempDir()
	program := filepath.Join(root, "infra/environment")
	if err := os.MkdirAll(program, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(program, "Pulumi.yaml"), []byte("name: premise-local-test\nruntime: yaml\noutputs:\n  marker: persistent\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := infraTestManifest()
	manifest.Workspace.Infra.Backends["remote"] = InfraBackend{URL: "file://" + t.TempDir(), AllowLocal: true}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	options := InfraOptions{Environment: "preview", Branch: "feature/state", Yes: true}
	manifest.Workspace.Infra.Environments["preview"] = InfraEnvironment{}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	ctx := context.Background()
	for _, operation := range []string{"init", "preview", "apply", "apply"} {
		if err := RunInfra(ctx, root, operation, options, nil, &output, &diagnostics); err != nil {
			t.Fatalf("%s: %v\n%s\n%s", operation, err, output.String(), diagnostics.String())
		}
	}
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := resolvePrerequisiteTargets(ctx, infraCommandRunner{stdout: &output, stderr: &diagnostics}, destination, []string{"*pulumi:pulumi:Stack*"}, nil)
	if err != nil {
		t.Fatalf("saved plan validation: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("empty saved plan targets")
	}
	output.Reset()
	if err := RunInfra(ctx, root, "output", options, nil, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(output.Bytes(), &values); err != nil {
		t.Fatalf("%v: %s", err, output.String())
	}
	if values["marker"] != "persistent" {
		t.Fatal(values)
	}
	if err := RunInfra(ctx, root, "destroy", options, nil, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
}

func TestInfrastructureRuntimeSecretsDoNotLeakThroughLogs(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "pulumi")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$APP_MARKER\" \"$VERCEL_TOKEN\"\nprintf '%s\\n' \"$APP_MARKER\" >&2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VERCEL_TOKEN", "administration-token-test")
	var output, diagnostics bytes.Buffer
	r := infraCommandRunner{stdout: &output, stderr: &diagnostics, secrets: map[string]string{"APP_MARKER": "runtime-secret-test"}}
	if err := r.run(context.Background(), directory, map[string]string{"APP_MARKER": "runtime-secret-test"}, "pulumi", "preview"); err != nil {
		t.Fatal(err)
	}
	text := output.String() + diagnostics.String()
	if strings.Contains(text, "runtime-secret-test") || strings.Contains(text, "administration-token-test") {
		t.Fatal("runtime/provider credential leaked")
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatal("no redaction evidence")
	}
}

func TestInfrastructureSecretsBelongToProgram(t *testing.T) {
	root := t.TempDir()
	manifest := infraTestManifest()
	// An app's secrets are not implicitly transported through an artifact binding.
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "web", Template: "registry:app", Path: "apps/web", Secrets: &ProjectSecrets{Config: "fnox.toml", Profiles: map[string]string{"dev": "dev"}, Keys: []string{"APP_MARKER"}}})
	manifest.Workspace.Infra.Bindings = map[string]InfraBinding{"web": {Program: "environment", Artifact: "."}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	values, err := infrastructureSecrets(t.Context(), root, InfraResolution{Program: manifest.Workspace.Projects[0]}, InfraOptions{Environment: "dev"})
	if err != nil || len(values) != 0 {
		t.Fatalf("unexpected implicit app secrets: %v %v", values, err)
	}
}

func TestBindInfrastructureWithoutBackendOrEnvironmentDeclarations(t *testing.T) {
	root := t.TempDir()
	manifest := infraTestManifest()
	manifest.Workspace.Infra = nil
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "web", Template: "registry:web", Path: "apps/web"})
	for _, path := range []string{"infra/environment", "apps/web"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := BindInfra(root, "web", "environment", "."); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULUMI_BACKEND_URL", "s3://client-owned-state")
	destination, err := ResolveInfrastructure(root, InfraOptions{Environment: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if destination.BackendURL != "s3://client-owned-state" || !strings.Contains(destination.Config["artifacts"], "apps/web") {
		t.Fatal("binding lost app path or ambient backend")
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
