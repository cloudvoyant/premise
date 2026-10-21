package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestDeploymentFailuresStopDependentPhasesAndPreservePreviousSuccess(t *testing.T) {
	for _, phase := range []string{"deploy", "health", "record"} {
		t.Run(phase, func(t *testing.T) {
			// Arrange.
			root, log, options := deploymentFailureFixture(t)
			var output bytes.Buffer
			if err := RunApplicationDeployment(t.Context(), root, options, nil, &output, &output); err != nil {
				t.Fatal(err)
			}

			// Act.
			t.Setenv("TEST_FAILURE_PHASE", phase)
			options.Release = "B"
			options.Artifacts["web"] = "artifact-B"
			if err := RunApplicationDeployment(t.Context(), root, options, nil, &output, &output); err == nil {
				t.Fatal("failure reported success")
			}

			// Assert.
			data, err := os.ReadFile(filepath.Join(root, ".premise/releases/environment/dev/last-run.json"))
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
				"init":      {"task migrate", "task deploy", "task health", "task rollback"},
				"migration": {"task deploy", "task health", "task rollback"},
				"apply":     {"task deploy", "task health"},
				"deploy":    {"task health"},
			}
			for _, event := range forbidden[phase] {
				if strings.Contains(b, event) {
					t.Fatalf("dependent stage ran after %s: %s", phase, b)
				}
			}
			if phase == "apply" || phase == "deploy" || phase == "health" || phase == "record" {
				if !strings.Contains(b, "task rollback release=B previous=A") {
					t.Fatal("previous release not restored", b)
				}
			}
			success, err := os.ReadFile(filepath.Join(root, ".premise/releases/environment/dev/last-success.json"))
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

func TestFailedRecoveryRetainsFailureAndSelectsFirstReleaseCleanup(t *testing.T) {
	for _, first := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous-release", true: "first-release"}[first], func(t *testing.T) {
			// Arrange.
			root, log, options := deploymentFailureFixture(t)
			var output bytes.Buffer
			if !first {
				if err := RunApplicationDeployment(t.Context(), root, options, nil, &output, &output); err != nil {
					t.Fatal(err)
				}
				options.Release = "B"
			}

			// Act.
			t.Setenv("TEST_FAILURE_PHASE", "health")
			t.Setenv("TEST_RECOVERY_FAILURE", "1")
			if err := RunApplicationDeployment(t.Context(), root, options, nil, &output, &output); err == nil {
				t.Fatal("recovery failure reported success")
			}

			// Assert.
			data, err := os.ReadFile(filepath.Join(root, ".premise/releases/environment/dev/last-run.json"))
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
				if _, err := os.Stat(filepath.Join(root, ".premise/releases/environment/dev/last-success.json")); !os.IsNotExist(err) {
					t.Fatal("first failed release recorded success")
				}
			}
		})
	}
}

func TestRecoveryRecordsAreNamespacedByProgram(t *testing.T) {
	root, _, _ := deploymentFailureFixture(t)
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	a, err := deploymentRecordDirectory(root, manifest, InfraResolution{ProjectName: "one", Stack: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := deploymentRecordDirectory(root, manifest, InfraResolution{ProjectName: "two", Stack: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different programs share release records")
	}
	for _, name := range []string{"..", "../other", "env*", "env?"} {
		if _, err := deploymentRecordDirectory(root, manifest, InfraResolution{ProjectName: name, Stack: "dev"}); err == nil {
			t.Fatalf("unsafe program namespace accepted: %s", name)
		}
	}

	destination, err := ResolveInfrastructure(root, InfraOptions{Environment: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := deploymentRecordStore(root, manifest, destination)
	if err != nil {
		t.Fatal(err)
	}
	if destination.ProjectName != "environment" || !strings.HasSuffix(store.URL, "/environment/dev") {
		t.Fatal(store.URL)
	}

}

func TestRecoveryRunsWithoutApplicationSecrets(t *testing.T) {
	root, log, options := deploymentFailureFixture(t)
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	for index := range manifest.Workspace.Projects {
		if manifest.Workspace.Projects[index].Name == "web" {
			manifest.Workspace.Projects[index].Secrets = &ProjectSecrets{Config: "not-created-yet.toml", Profiles: map[string]string{"dev": "dev"}, Keys: []string{"DATABASE_URL"}}
		}
	}
	var output bytes.Buffer
	err = runDeploymentTask(t.Context(), root, manifest, TaskReference{Project: "web", Task: "cleanup"}, map[string]string{"PM_FAILED_PHASE": "migration", "PM_RELEASE": "A"}, infraCommandRunner{stdout: &output, stderr: &output}, options)
	if err != nil {
		t.Fatal(err)
	}
	events, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(events), "task cleanup") {
		t.Fatalf("cleanup did not run: %v", err)
	}
}

// Private helpers ------------------------------------------------------------

func deploymentFailureFixture(t *testing.T) (string, string, DeployOptions) {
	t.Helper()
	root := t.TempDir()
	prepareInfraTestProject(t, root)
	for _, dir := range []string{"infra/environment", "apps/web", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := infraTestManifest()
	if err := os.MkdirAll(filepath.Join(root, "apps/web"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps/web/mise.toml"), []byte("[tasks.migrate]\nrun = 'echo migration'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: "web", Template: "registry/app", Path: "apps/web"})
	manifest.Workspace.Deployment = &Deployment{
		Records:       ".premise/releases",
		RecordOutputs: []string{"deploymentId"},
		Applications: []DeploymentApplication{{
			Project:  "web",
			Deploy:   "deploy",
			Health:   "health",
			Rollback: "rollback",
		}},
		Recovery: DeploymentRecovery{
			FirstDeploy: &TaskReference{Project: "web", Task: "cleanup"},
		},
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	pulumi, err := os.ReadFile("testdata/deployment/pulumi.sh")
	if err != nil {
		t.Fatal(err)
	}

	mise, err := os.ReadFile("testdata/deployment/mise.sh")
	if err != nil {
		t.Fatal(err)
	}

	for name, script := range map[string][]byte{"pulumi": pulumi, "mise": mise} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), script, 0700); err != nil {
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
