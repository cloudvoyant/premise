package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestDeploymentRollbackPreservesLastSuccessfulRelease(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"infra/environment", "apps/web", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := infraTestManifest()
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "web", Template: "registry/app", Path: "apps/web"})
	manifest.Workspace.Deployment = &Deployment{Records: ".premise/releases", Prerequisites: []string{"urn:db"}, Migrations: []TaskReference{{Project: "web", Task: "migrate"}}, Applications: []DeploymentApplication{{Project: "web", ManagedBy: "infra", Health: "health", Rollback: "rollback"}}, Recovery: DeploymentRecovery{FirstDeploy: &TaskReference{Project: "web", Task: "cleanup"}}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	pulumi := `#!/bin/sh
printf 'pulumi %s\n' "$*" >> "$TEST_DEPLOY_LOG"
previous=""
for argument in "$@"; do
 if [ "$previous" = "--save-plan" ]; then printf '%s' '{"version":1,"plan":{"resourcePlans":{"urn:db":{"goal":{"custom":true,"type":"test:db"}}}}}' > "$argument"; fi
 previous="$argument"
done
`
	mise := `#!/bin/sh
printf 'task %s release=%s previous=%s\n' "$2" "$PM_RELEASE" "$PM_PREVIOUS_RELEASE" >> "$TEST_DEPLOY_LOG"
if [ "$2" = "health" ] && [ "$PM_RELEASE" = "B" ]; then exit 1; fi
`
	for name, script := range map[string]string{"pulumi": pulumi, "mise": mise} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(root, "events")
	t.Setenv("TEST_DEPLOY_LOG", log)
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	var output bytes.Buffer
	options := DeployOptions{InfraOptions: InfraOptions{Environment: "dev"}, Release: "A", Artifacts: map[string]string{"web": "artifact-A"}}
	if err := RunDeployment(context.Background(), root, options, nil, &output, &output); err != nil {
		t.Fatal(err)
	}
	options.Release = "B"
	options.Artifacts["web"] = "artifact-B"
	if err := RunDeployment(context.Background(), root, options, nil, &output, &output); err == nil {
		t.Fatal("failed health reported success")
	}
	data, err := os.ReadFile(filepath.Join(root, ".premise/releases/dev/last-success.json"))
	if err != nil {
		t.Fatal(err)
	}
	var last ReleaseRecord
	if err := json.Unmarshal(data, &last); err != nil {
		t.Fatal(err)
	}
	if last.Release != "A" {
		t.Fatal(last)
	}
	data, err = os.ReadFile(filepath.Join(root, ".premise/releases/dev/last-run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result DeploymentResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Succeeded || !result.RecoverySucceeded {
		t.Fatal(result)
	}
	events, _ := os.ReadFile(log)
	text := string(events)
	if !strings.Contains(text, "task rollback release=B previous=A") {
		t.Fatal(text)
	}
	rollback := strings.Index(text, "task rollback")
	if strings.Contains(text[rollback:], "task migrate") {
		t.Fatal("rollback reran migrations")
	}
	if strings.Count(text, "--target urn:db") != 4 {
		t.Fatalf("expected targeted preview/apply once per release: %s", text)
	}
}

func TestDeploymentFailureGatesAndRecovery(t *testing.T) {
	for _, phase := range []string{"provision", "migration", "apply", "deploy", "health"} {
		t.Run(phase, func(t *testing.T) {
			root, log, options := deploymentFailureFixture(t)
			var output bytes.Buffer
			if err := RunDeployment(t.Context(), root, options, nil, &output, &output); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TEST_FAILURE_PHASE", phase)
			options.Release = "B"
			options.Artifacts["web"] = "artifact-B"
			if err := RunDeployment(t.Context(), root, options, nil, &output, &output); err == nil {
				t.Fatal("failure reported success")
			}
			data, err := os.ReadFile(filepath.Join(root, ".premise/releases/dev/last-run.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result DeploymentResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Phase != phase || result.Succeeded || !result.RecoverySucceeded {
				t.Fatalf("result = %+v", result)
			}
			events, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			b := string(events)[strings.Index(string(events), "release=B"):]
			forbidden := map[string][]string{
				"provision": {"task migrate", "task deploy", "task health", "task rollback"},
				"migration": {"task deploy", "task health", "task rollback"},
				"apply":     {"task deploy", "task health"},
				"deploy":    {"task health"},
			}
			for _, event := range forbidden[phase] {
				if strings.Contains(b, event) {
					t.Fatalf("dependent stage ran after %s: %s", phase, b)
				}
			}
			if phase == "apply" || phase == "deploy" || phase == "health" {
				if !strings.Contains(b, "task rollback release=B previous=A") {
					t.Fatal("previous release not restored", b)
				}
			}
			success, err := os.ReadFile(filepath.Join(root, ".premise/releases/dev/last-success.json"))
			if err != nil {
				t.Fatal(err)
			}
			var last ReleaseRecord
			if err := json.Unmarshal(success, &last); err != nil {
				t.Fatal(err)
			}
			if last.Release != "A" {
				t.Fatal("failed release replaced success", last)
			}
		})
	}
}

func TestDeploymentRecoveryFailureAndFirstReleaseCleanup(t *testing.T) {
	for _, first := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous-release", true: "first-release"}[first], func(t *testing.T) {
			root, log, options := deploymentFailureFixture(t)
			var output bytes.Buffer
			if !first {
				if err := RunDeployment(t.Context(), root, options, nil, &output, &output); err != nil {
					t.Fatal(err)
				}
				options.Release = "B"
			}
			t.Setenv("TEST_FAILURE_PHASE", "health")
			t.Setenv("TEST_RECOVERY_FAILURE", "1")
			if err := RunDeployment(t.Context(), root, options, nil, &output, &output); err == nil {
				t.Fatal("recovery failure reported success")
			}
			data, err := os.ReadFile(filepath.Join(root, ".premise/releases/dev/last-run.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result DeploymentResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Succeeded || !result.RecoveryAttempted || result.RecoverySucceeded {
				t.Fatal(result)
			}
			events, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			expected := "task rollback release=B previous=A"
			if first {
				expected = "task cleanup release=A previous="
			}
			if !strings.Contains(string(events), expected) {
				t.Fatal("wrong recovery task", string(events))
			}
			if first {
				if _, err := os.Stat(filepath.Join(root, ".premise/releases/dev/last-success.json")); !os.IsNotExist(err) {
					t.Fatal("first failed release recorded success")
				}
			}
		})
	}
}

// Private helpers ------------------------------------------------------------

func deploymentFailureFixture(t *testing.T) (string, string, DeployOptions) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"infra/environment", "apps/web", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := infraTestManifest()
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "web", Template: "registry/app", Path: "apps/web"})
	manifest.Workspace.Deployment = &Deployment{Records: ".premise/releases", Prerequisites: []string{"urn:db"}, Migrations: []TaskReference{{Project: "web", Task: "migrate"}}, Applications: []DeploymentApplication{{Project: "web", ManagedBy: "task", Deploy: "deploy", Health: "health", Rollback: "rollback"}}, Recovery: DeploymentRecovery{FirstDeploy: &TaskReference{Project: "web", Task: "cleanup"}}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	pulumi := `#!/bin/sh
printf 'pulumi release=%s %s\n' "$PM_RELEASE" "$*" >> "$TEST_DEPLOY_LOG"
previous=""
for argument in "$@"; do
 if [ "$previous" = "--save-plan" ]; then printf '%s' '{"resourcePlans":{"urn:db":{"goal":{"custom":true,"type":"test:db"}}}}' > "$argument"; fi
 previous="$argument"
done
if [ "$1" = "up" ]; then
 case "$*" in
  *--target*) [ "$TEST_FAILURE_PHASE" != "provision" ] || exit 1;;
  *) [ "$TEST_FAILURE_PHASE" != "apply" ] || exit 1;;
 esac
fi
`
	mise := `#!/bin/sh
printf 'task %s release=%s previous=%s\n' "$2" "$PM_RELEASE" "$PM_PREVIOUS_RELEASE" >> "$TEST_DEPLOY_LOG"
failed_task="$TEST_FAILURE_PHASE"
[ "$failed_task" != "migration" ] || failed_task="migrate"
[ "$2" != "$failed_task" ] || exit 1
case "$2" in rollback|cleanup) [ "$TEST_RECOVERY_FAILURE" != "1" ] || exit 1;; esac
exit 0
`
	for name, script := range map[string]string{"pulumi": pulumi, "mise": mise} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(root, "events")
	t.Setenv("TEST_DEPLOY_LOG", log)
	t.Setenv("TEST_FAILURE_PHASE", "")
	t.Setenv("TEST_RECOVERY_FAILURE", "")
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, log, DeployOptions{InfraOptions: InfraOptions{Environment: "dev"}, Release: "A", Artifacts: map[string]string{"web": "artifact-A"}}
}
