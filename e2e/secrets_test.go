package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestSecretsPullSelectsEnvironmentAndWritesPrivateIgnoredFile(t *testing.T) {
	// Arrange.
	if os.Getenv("PREMISE_TEST_FNOX") != "1" {
		t.Skip("set PREMISE_TEST_FNOX=1 to use real fnox")
	}
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI"} {
		t.Setenv(key, "")
	}
	binary := premiseBinary(t)
	client := filepath.Join(t.TempDir(), "client")
	copyInfraFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/client"), client)
	infraCommand(t, client, "git", "init", "--quiet")
	destination := filepath.Join(client, "apps/web/.env.local")

	// Act and assert.
	for _, environment := range []string{"dev", "stage", "prod", "preview"} {
		arguments := []string{"secrets", "pull", "--project", "web", "--env", environment}
		if environment == "preview" {
			arguments = append(arguments, "--branch", "feature/secrets")
		}
		output := infraCommand(t, client, binary, arguments...)
		value := environment + "-example"
		if strings.Contains(output, value) {
			t.Fatal("secrets pull printed an application value")
		}
		data, err := os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "APP_MESSAGE=\""+value+"\"") {
			t.Fatalf("wrong %s profile in env file", environment)
		}
		info, err := os.Stat(destination)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("environment file must be private")
		}
		infraCommand(t, client, "git", "check-ignore", "--quiet", "--", destination)
	}
	before, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "secrets", "pull", "--project", "web", "--env", "missing")
	command.Dir = client
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("unknown environment succeeded: %s", output)
	}
	after, err := os.ReadFile(destination)
	if err != nil || string(after) != string(before) {
		t.Fatal("failed pull changed the existing file")
	}
}
