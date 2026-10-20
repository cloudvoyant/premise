package providers

import (
	"context"
	"io"
)

// Neon validates credentials supplied by the user or CI.
type Neon struct{}

// Public API -----------------------------------------------------------------

// EnsureEnvironment checks the provider's standard API token variable.
func (Neon) EnsureEnvironment(_ bool) error { return requireVariables("neon", "NEON_API_KEY") }

// Authenticate validates the existing API token without interactive login.
func (p Neon) Authenticate(_ context.Context, _ string, ci bool, _ io.Reader, _, _ io.Writer) (map[string]string, error) {
	return nil, p.EnsureEnvironment(ci)
}

// Private helpers ------------------------------------------------------------

var neonSecretKeys = []string{"NEON_API_KEY", "NEON_API_TOKEN"}
