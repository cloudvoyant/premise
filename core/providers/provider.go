// Package providers contains provider-specific authentication and credential policy.
package providers

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Public API -----------------------------------------------------------------

// AdministrationSecretKeys returns credential names that must not reach application tasks.
func AdministrationSecretKeys() map[string]bool {
	keys := map[string]bool{}
	for _, key := range slices.Concat(awsSecretKeys, githubSecretKeys, gitlabSecretKeys, pulumiSecretKeys) {
		keys[key] = true
	}
	return keys
}

// SyncCISecret sends a local scalar value to the chosen CI secret store over stdin.
func SyncCISecret(ctx context.Context, root, provider, repository, environment, key, value string) error {
	switch provider {
	case "github":
		return syncGitHubSecret(ctx, root, repository, environment, key, value)
	case "gitlab":
		return syncGitLabSecret(ctx, root, repository, environment, key, value)
	default:
		return fmt.Errorf("secrets sync supports github or gitlab")
	}
}

// Private helpers ------------------------------------------------------------

func requireVariables(provider string, keys ...string) error {
	var missing []string
	for _, key := range keys {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: missing %s; configure CI variables as described in docs/providers/%s.md", provider, strings.Join(missing, ", "), provider)
	}
	return nil
}

func runCommand(ctx context.Context, root string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = root
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s operation failed: %w", name, err)
	}
	return nil
}
