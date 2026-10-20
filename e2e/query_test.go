package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Public API -----------------------------------------------------------------

func TestInfraQueriesPublishedResourcesAcrossLanguagesWithoutExposingSecrets(t *testing.T) {
	if os.Getenv("PREMISE_TEST_PULUMI") != "1" {
		t.Skip("set PREMISE_TEST_PULUMI=1 for real Go and TypeScript resource queries")
	}
	binary := premiseBinary(t)
	for _, language := range []string{"go", "typescript"} {
		t.Run(language, func(t *testing.T) {
			// Arrange.
			t.Setenv("PULUMI_HOME", t.TempDir())
			root := filepath.Join(t.TempDir(), "client")
			copyInfraFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/query", language), root)
			configureLocalInfrastructure(t, root)
			// A global binary must never override the workspace Mise pin.
			poison := t.TempDir()
			if err := os.WriteFile(filepath.Join(poison, "pulumi"), []byte("#!/bin/sh\necho 'global pulumi must not run' >&2\nexit 91\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", poison+string(os.PathListSeparator)+os.Getenv("PATH"))
			if version := strings.TrimSpace(infraCommand(t, root, "mise", "exec", "--", "pulumi", "version")); version != "v3.268.0" {
				t.Fatalf("wrong workspace Pulumi: %s", version)
			}
			infraCommand(t, root, "mise", "run", "infra:install")
			scope := []string{"--env", "preview", "--branch", "feature/query"}
			run := func(operation string, args ...string) string {
				return infraCommand(t, root, binary, append(append([]string{"infra", operation}, scope...), args...)...)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				command := exec.CommandContext(ctx, binary, append([]string{"infra", "destroy"}, scope...)...)
				command.Dir = root
				command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+root)
				if output, err := command.CombinedOutput(); err != nil {
					t.Errorf("query fixture cleanup: %v\n%s", err, output)
				}
			})

			// Act.
			run("init")
			run("plan")
			run("apply", "--yes")
			id := strings.TrimSpace(run("query", "--output", "publicId"))
			property := strings.TrimSpace(run("query", "--resource", "duplicate", "--type", "random:index/randomId:RandomId", "--property", "hex"))
			fresh := filepath.Join(t.TempDir(), "client")
			copyInfraFixture(t, root, fresh)
			reloaded := strings.TrimSpace(infraCommand(t, fresh, binary, append(append([]string{"infra", "query"}, scope...), "--output", "publicId")...))

			// Assert.
			if id == "" || id != property || id != reloaded {
				t.Fatalf("resource/state identity mismatch: output=%q property=%q reloaded=%q", id, property, reloaded)
			}
			var encoded string
			if err := json.Unmarshal([]byte(run("query", "--output", "publicId", "--json")), &encoded); err != nil || encoded != id {
				t.Fatalf("JSON scalar mismatch: %q, %v", encoded, err)
			}
			for _, selection := range [][]string{
				{"--output", "missing"},
				{"--resource", "missing", "--property", "id"},
				{"--resource", "duplicate", "--property", "id"},
				{"--resource", "duplicate", "--type", "random:index/randomId:RandomId", "--property", "missing"},
				{"--output", "privateValue"},
			} {
				command := exec.CommandContext(t.Context(), binary, append(append([]string{"infra", "query"}, scope...), selection...)...)
				command.Dir = root
				command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+root)
				output, err := command.CombinedOutput()
				if err == nil || strings.Contains(string(output), "query-fixture-secret") {
					t.Fatalf("invalid or secret query did not fail safely: %v\n%s", selection, output)
				}
			}
			if secret := strings.TrimSpace(run("query", "--output", "privateValue", "--show-secrets")); secret != "query-fixture-secret" {
				t.Fatalf("explicit secret query did not return requested value")
			}
		})
	}
}
