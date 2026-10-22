package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestSecrets(t *testing.T) {
	if os.Getenv("PREMISE_TEST_FNOX") != "1" {
		t.Skip("set PREMISE_TEST_FNOX=1 to use real fnox")
	}
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI"} {
		t.Setenv(key, "")
	}
	binary := premiseBinary(t)

	t.Run("when creating env files", func(t *testing.T) {
		for _, environment := range []string{"dev", "stage", "prod", "preview"} {
			t.Run("for "+environment, func(t *testing.T) {
				// Arrange.
				client := secretsClient(t)
				destination := filepath.Join(client, "apps/web/.env.local")
				args := []string{"secrets", "pull", "--project", "web", "--env", environment}
				if environment == "preview" {
					args = append(args, "--branch", "feature/secrets")
				}

				// Act.
				output := runCommand(t, client, binary, args...)

				// Assert.
				t.Run("it selects the matching JSON secret profile", func(t *testing.T) {
					assertFileContains(t, destination, "APP_MESSAGE=\""+environment+"-example\"")
				})
				t.Run("it writes an ignored private file", func(t *testing.T) {
					info, err := os.Stat(destination)
					if err != nil || info.Mode().Perm() != 0600 {
						t.Fatal("env file must have mode 0600")
					}
					runCommand(t, client, "git", "check-ignore", "--quiet", "--", destination)
				})
				t.Run("it keeps the secret out of command output", func(t *testing.T) {
					if strings.Contains(output, environment+"-example") {
						t.Fatal("secrets pull printed an application secret")
					}
				})
			})
		}

		t.Run("when the selected profile is missing", func(t *testing.T) {
			// Arrange.
			client := secretsClient(t)
			destination := filepath.Join(client, "apps/web/.env.local")
			runCommand(t, client, binary, "secrets", "pull", "--project", "web", "--env", "dev")
			before := readFile(t, destination)
			configPath := filepath.Join(client, "apps/web/fnox.toml")
			config := strings.Replace(readFile(t, configPath), "[profiles.stage.secrets]", "[profiles.unavailable.secrets]", 1)
			if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}

			// Act.
			commandFailure(t, client, binary, "secrets", "pull", "--project", "web", "--env", "stage")

			// Assert.
			t.Run("it preserves the previous env file", func(t *testing.T) { assertFileContent(t, destination, before) })
		})
	})

	t.Run("when running Mise tasks", func(t *testing.T) {
		// Arrange.
		client := secretsClient(t)
		runCommand(t, client, binary, "secrets", "pull", "--project", "web", "--env", "stage")

		// Act.
		output := runCommand(t, client, binary, "run", "web:show-message")

		// Assert.
		t.Run("it makes app secrets available in the task environment", func(t *testing.T) {
			if output != "stage-example" {
				t.Fatalf("task received %q, want stage profile", output)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func secretsClient(t *testing.T) string {
	t.Helper()
	client := filepath.Join(t.TempDir(), "client")
	copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/client"), client)
	copyFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/secrets"), filepath.Join(client, "apps/web"))
	runCommand(t, client, "git", "init", "--quiet")
	return client
}
