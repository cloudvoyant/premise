package core

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestApplicationFailuresCallClientRollbackAndKeepTheFailure(t *testing.T) {
	for _, phase := range []string{"deploy", "health"} {
		t.Run(phase, func(t *testing.T) {
			// Arrange.
			root, log, options := deploymentFailureFixture(t)
			t.Setenv("TEST_FAILURE_PHASE", phase)
			var output bytes.Buffer

			// Act.
			err := RunApplicationDeployment(t.Context(), root, options, nil, &output, &output)

			// Assert.
			if err == nil || !strings.Contains(err.Error(), phase) {
				t.Fatalf("application failure was lost: %v", err)
			}
			data, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			events := string(data)
			if !strings.Contains(events, "task rollback release=A") {
				t.Fatal("client rollback did not run", events)
			}
			if strings.Contains(events, "pulumi ") {
				t.Fatal("application rollback changed infrastructure", events)
			}
			if _, err := os.Stat(filepath.Join(root, ".premise")); !os.IsNotExist(err) {
				t.Fatal("deployment created a custom record store")
			}
		})
	}
}

func TestRollbackFailureReportsBothHealthAndRollbackErrors(t *testing.T) {
	// Arrange.
	root, _, options := deploymentFailureFixture(t)
	t.Setenv("TEST_FAILURE_PHASE", "health")
	t.Setenv("TEST_ROLLBACK_FAILURE", "1")

	// Act.
	err := RunApplicationDeployment(t.Context(), root, options, nil, io.Discard, io.Discard)

	// Assert.
	if err == nil || !strings.Contains(err.Error(), "health") || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("missing original or rollback failure: %v", err)
	}
}

func TestEnvironmentFlowOrdersOptionalMigrationsAroundTheFirstApply(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, migrations := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/migrations=%t", existing, migrations), func(t *testing.T) {
				// Arrange.
				root, log, _ := deploymentFailureFixture(t)
				t.Setenv("TEST_INFRA_EXISTS", map[bool]string{false: "0", true: "1"}[existing])
				if !migrations {
					if err := os.WriteFile(filepath.Join(root, "apps/web/mise.toml"), []byte("[tasks.build]\nrun = 'true'\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}

				// Act.
				err := RunCIFlow(t.Context(), root, CIFlowOnDeploy, "dev", CIReleaseNone, io.Discard, io.Discard)

				// Assert.
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				events := string(data)
				migration, apply, deploy := strings.Index(events, "task migrate"), strings.Index(events, "up --stack dev"), strings.Index(events, "task deploy")
				if apply < 0 || deploy < apply {
					t.Fatal("infra apply did not precede deployment", events)
				}
				if !migrations && migration >= 0 {
					t.Fatal("ran an absent migration task", events)
				}
				if migrations && (migration < 0 || (existing && migration > apply) || (!existing && migration < apply) || deploy < migration) {
					t.Fatal("incorrect migration ordering", events)
				}
			})
		}
	}
}

func TestInfrastructureAndMigrationFailuresStopWithoutApplicationRollback(t *testing.T) {
	for _, phase := range []string{"init", "apply", "migration"} {
		t.Run(phase, func(t *testing.T) {
			// Arrange.
			root, log, _ := deploymentFailureFixture(t)
			t.Setenv("TEST_INFRA_EXISTS", "1")
			t.Setenv("TEST_FAILURE_PHASE", phase)

			// Act.
			err := RunCIFlow(t.Context(), root, CIFlowOnDeploy, "dev", CIReleaseNone, io.Discard, io.Discard)

			// Assert.
			if err == nil {
				t.Fatal("failed update reported success")
			}
			data, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(data), "task rollback") || strings.Contains(string(data), "task deploy") || strings.Contains(string(data), "refresh") {
				t.Fatal("failed infra or migration triggered application recovery", string(data))
			}
		})
	}
}

func TestMergeFlowDeploysDevAndOnlyDestroysItsSelectedPreview(t *testing.T) {
	for _, environment := range []string{"dev", "preview"} {
		t.Run(environment, func(t *testing.T) {
			// Arrange.
			root, log, _ := deploymentFailureFixture(t)
			branch := ""
			if environment == "preview" {
				branch = "feature/cleanup"
			}
			stack, err := ResolveStackName(InfraOptions{Environment: environment, Branch: branch})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("TEST_PREVIEW_STACK", stack)

			// Act.
			err = RunCIFlowWithOptions(t.Context(), root, CIFlowOnMerge, environment, CIReleaseNone, CIFlowOptions{Branch: branch}, io.Discard, io.Discard)

			// Assert.
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if environment == "dev" && !strings.Contains(string(data), "task deploy") {
				t.Fatal("merge did not deploy dev", string(data))
			}
			if environment == "preview" && (!strings.Contains(string(data), "destroy --stack "+stack) || strings.Contains(string(data), "task deploy")) {
				t.Fatal("merge preview cleanup did not stay scoped", string(data))
			}
		})
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
		Applications: []DeploymentApplication{{
			Project:  "web",
			Deploy:   "deploy",
			Health:   "health",
			Rollback: "rollback",
		}},
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
	t.Setenv("TEST_ROLLBACK_FAILURE", "")
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, log, DeployOptions{InfraOptions: InfraOptions{Environment: "dev"}, Release: "A"}
}
