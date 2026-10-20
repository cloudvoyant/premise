package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestStackNamesSeparateEnvironmentsAndAvoidPreviewCollisions(t *testing.T) {
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
			got, err := ResolveStackName(tc.options)
			if (err == nil) != tc.valid || got != tc.want {
				t.Fatalf("got %q, %v; want %q, valid=%t", got, err, tc.want, tc.valid)
			}
		})
	}
	a, _ := ResolveStackName(InfraOptions{Environment: "preview", Branch: "feature/a"})
	repeated, _ := ResolveStackName(InfraOptions{Environment: "preview", Branch: "feature/a"})
	b, _ := ResolveStackName(InfraOptions{Environment: "preview", Branch: "feature-a"})
	if a != repeated || a == b {
		t.Fatal("preview identity is unstable or collides")
	}
}

func TestBackendValidationAllowsRemoteStoresAndRequiresExplicitLocalAccess(t *testing.T) {
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

func TestPreviewCleanupHandlesMissingStacksWithoutHidingBackendFailures(t *testing.T) {
	for _, tc := range []struct {
		name, listing, status string
		wantDestroy, wantErr  bool
	}{
		{"already removed", `[]`, "0", false, false},
		{"another environment", `[{"name":"prod"}]`, "0", false, false},
		{"matching preview", "matching", "0", true, false},
		{"qualified preview", "qualified", "0", true, false},
		{"backend denied", `[]`, "1", false, true},
		{"invalid response", `not json`, "0", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			prepareInfraTestProject(t, root)
			if err := os.MkdirAll(filepath.Join(root, "infra/environment"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := SaveManifest(filepath.Join(root, ManifestFilename), infraTestManifest()); err != nil {
				t.Fatal(err)
			}
			options := InfraOptions{Environment: "preview", Branch: "feature/cleanup"}
			stack, err := ResolveStackName(options)
			if err != nil {
				t.Fatal(err)
			}
			listing := tc.listing
			if listing == "matching" {
				listing = `[{"name":"` + stack + `"}]`
			}
			if listing == "qualified" {
				listing = `[{"name":"organization/environment/` + stack + `"}]`
			}
			bin := t.TempDir()
			log := filepath.Join(t.TempDir(), "destroyed")
			script := "#!/bin/sh\ncase \"$1 $2\" in\n  'stack ls') printf '%s' \"$STACK_LIST\"; exit \"$LIST_STATUS\";;\nesac\nif [ \"$1\" = destroy ]; then touch \"$DESTROY_LOG\"; fi\n"
			if err := os.WriteFile(filepath.Join(bin, "pulumi"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "mise"), []byte("#!/bin/sh\n[ \"$1 $2 $3\" = 'exec -- pulumi' ] || exit 9\nshift 3\nexec pulumi \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("STACK_LIST", listing)
			t.Setenv("LIST_STATUS", tc.status)
			t.Setenv("DESTROY_LOG", log)
			err = RunInfra(t.Context(), root, "destroy", options, nil, nil, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("cleanup error = %v", err)
			}
			_, err = os.Stat(log)
			if (err == nil) != tc.wantDestroy {
				t.Fatalf("destroyed=%t, want %t", err == nil, tc.wantDestroy)
			}
		})
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

// Private helpers ------------------------------------------------------------

func infraTestManifest() Config {
	manifest := NewManifest("example")
	manifest.Workspace.Infra = &Infrastructure{Backend: "remote", Backends: map[string]InfraBackend{"remote": {URL: "gs://test"}}, Environments: map[string]InfraEnvironment{"dev": {}}}
	manifest.Workspace.Infra.Directory = "infra/environment"
	return manifest
}

type recordingInfraRunner struct{ commands [][]string }

func (r *recordingInfraRunner) run(_ context.Context, _ string, _ map[string]string, name string, args ...string) error {
	r.commands = append(r.commands, append([]string{name}, args...))
	return nil
}

func prepareInfraTestProject(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "infra/environment")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "Pulumi.yaml"), []byte("name: environment\nruntime: yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte(`[tools]
pulumi = "3.268.0"
`), 0600); err != nil {
		t.Fatal(err)
	}
}
