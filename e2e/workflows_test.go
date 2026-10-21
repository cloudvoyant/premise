package e2e_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Public API -----------------------------------------------------------------

func TestWorkflowContractPreservesReleaseOrderManualDeployAndMergeCleanup(t *testing.T) {
	// Arrange.
	root := repositoryRoot(t)
	allowed := []string{"on-commit.yml", "on-deploy.yml", "on-merge.yml"}
	for _, directory := range []string{".github/workflows", "assets/github-workflows"} {
		t.Run(directory, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(root, directory))
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name())
			}

			// Act and assert.
			if !reflect.DeepEqual(names, allowed) {
				t.Fatalf("workflows = %v, want only %v", names, allowed)
			}
			for _, name := range allowed {
				data, err := os.ReadFile(filepath.Join(root, directory, name))
				if err != nil {
					t.Fatal(err)
				}
				var workflow struct {
					Events map[string]any `yaml:"on"`
					Jobs   map[string]struct {
						Needs    []string `yaml:"needs"`
						If       string   `yaml:"if"`
						Strategy struct {
							FailFast *bool `yaml:"fail-fast"`
						} `yaml:"strategy"`
					} `yaml:"jobs"`
				}
				// Normalize scalar dependencies to lists before decoding.
				lines := strings.Split(string(data), "\n")
				for i, line := range lines {
					if value, ok := strings.CutPrefix(line, "    needs: "); ok && !strings.HasPrefix(value, "[") {
						lines[i] = "    needs: [" + value + "]"
					}
				}
				if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &workflow); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "workflow_call") || strings.Contains(string(data), "e2e:") || strings.Contains(string(data), "PREMISE_TEST_") {
					t.Fatalf("%s adds a reusable workflow or infrastructure validation", name)
				}
				if name == "on-merge.yml" {
					if _, ok := workflow.Jobs["cleanup-preview"]; !ok {
						t.Fatal("preview cleanup must remain in on-merge")
					}
					if _, ok := workflow.Events["delete"]; !ok {
						t.Fatal("preview cleanup lost its branch-deletion fallback")
					}
					for _, fragment := range []string{"github.event.pull_request.merged == true", "github.event_name == 'delete'", "github.event.ref_type == 'branch'"} {
						if !strings.Contains(workflow.Jobs["cleanup-preview"].If, fragment) {
							t.Fatalf("merge cleanup lost condition %s", fragment)
						}
					}
					continue
				}
				if _, ok := workflow.Jobs["cleanup-preview"]; ok {
					t.Fatalf("%s must not own preview cleanup", name)
				}
				if name == "on-deploy.yml" && (len(workflow.Events) != 1 || workflow.Events["workflow_dispatch"] == nil) {
					t.Fatal("on-deploy must remain manual-only")
				}
				for job, needs := range map[string][]string{
					"migrate": {"provision"},
					"finish":  {"provision", "migrate"},
					"recover": {"provision", "migrate", "finish"},
				} {
					if !reflect.DeepEqual(workflow.Jobs[job].Needs, needs) {
						t.Fatalf("%s %s dependencies = %v, want %v", name, job, workflow.Jobs[job].Needs, needs)
					}
				}
				if fast := workflow.Jobs["migrate"].Strategy.FailFast; fast == nil || *fast {
					t.Fatal("migration fan-out must wait for every worker before recovery")
				}
				if name == "on-commit.yml" {
					for _, dependency := range []string{"plan", "single", "matrix", "publish-release", "publish-packages"} {
						if !slices.Contains(workflow.Jobs["provision"].Needs, dependency) {
							t.Fatalf("preview provisioning must wait for %s", dependency)
						}
					}
				}
			}
		})
	}
}
