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

// InfraProvider authenticates deployment tools and checks their standard variables.
// It never resolves application secrets.
type InfraProvider interface {
	EnsureEnvironment(ci bool) error
	Authenticate(context.Context, string, bool, io.Reader, io.Writer, io.Writer) (map[string]string, error)
}

// CIProvider owns runner output formatting and identity exchange.
type CIProvider interface {
	WritePlan(io.Writer, CIPlan) error
	IdentityToken(context.Context, string, string) error
}

// Public API -----------------------------------------------------------------

// InfrastructureProvider returns a supported provider, independently of Pulumi resources.
func InfrastructureProvider(name string) (InfraProvider, bool) {
	switch name {
	case "gcp":
		return GCP{}, true
	default:
		return nil, false
	}
}

// AdministrationSecretKeys returns credential names that must not reach application tasks.
func AdministrationSecretKeys() map[string]bool {
	keys := map[string]bool{}
	for _, key := range slices.Concat(githubSecretKeys, gitlabSecretKeys, pulumiSecretKeys) {
		keys[key] = true
	}
	return keys
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
