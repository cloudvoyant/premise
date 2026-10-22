package e2e_test

import (
	"encoding/json"
	"fmt"
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
	task := `[tasks."secrets:pull"]
run = "python3 -c 'import json,os; print(\"APP_MESSAGE=\"+json.dumps(json.load(open(os.environ[\"APP_CONFIG\"]))[\"message\"]))'"
`
	if err := os.WriteFile(filepath.Join(client, "apps/web/mise.toml"), []byte(task), 0600); err != nil {
		t.Fatal(err)
	}

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

func TestSecretsExecUsesJSONFilesInLocalAndCIAndCleansThemUp(t *testing.T) {
	// Arrange.
	if os.Getenv("PREMISE_TEST_FNOX") != "1" {
		t.Skip("set PREMISE_TEST_FNOX=1 to use real fnox")
	}
	binary := premiseBinary(t)
	client := filepath.Join(t.TempDir(), "client")
	copyInfraFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/client"), client)
	for _, ci := range []string{"", "true"} {
		t.Setenv("CI", ci)
		for _, environment := range []string{"dev", "stage", "prod", "preview"} {
			// Act.
			script := "import json,os,stat; p=os.environ['APP_CONFIG']; print(json.dumps({'message':json.load(open(p))['message'],'path':p,'mode':stat.S_IMODE(os.stat(p).st_mode)}))"
			args := []string{"secrets", "exec", "--project", "web", "--env", environment}
			if environment == "preview" {
				args = append(args, "--branch", "feature/secrets")
			}
			args = append(args, "--", "python3", "-c", script)
			output := infraCommand(t, client, binary, args...)

			// Assert.
			var result struct {
				Message, Path string
				Mode          int
			}
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatal(err)
			}
			if result.Message != environment+"-example" || result.Mode != 0600 {
				t.Fatalf("incorrect JSON file for %s (CI=%s): %+v", environment, ci, result)
			}
			if _, err := os.Stat(result.Path); !os.IsNotExist(err) {
				t.Fatal("fnox left a secret file after the child exited")
			}
		}
	}
	// Failures propagate and fnox still owns temporary-file cleanup.
	marker := filepath.Join(t.TempDir(), "path.txt")
	script := fmt.Sprintf("import os,pathlib,sys; pathlib.Path(%q).write_text(os.environ['APP_CONFIG']); sys.exit(7)", marker)
	command := exec.Command(binary, "secrets", "exec", "--project", "web", "--env", "dev", "--", "python3", "-c", script)
	command.Dir = client
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+client)
	if _, err := command.CombinedOutput(); err == nil {
		t.Fatal("child failure reported success")
	}
	path, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
		t.Fatal("failed child left its JSON secret file")
	}
}
