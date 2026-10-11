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
