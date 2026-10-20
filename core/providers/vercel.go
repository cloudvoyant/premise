package providers

import (
	"context"
	"io"
)

// Vercel validates credentials supplied by the user or CI.
type Vercel struct{}

// Public API -----------------------------------------------------------------

// EnsureEnvironment checks the provider's standard API token variable.
func (Vercel) EnsureEnvironment(_ bool) error { return requireVariables("vercel", "VERCEL_API_TOKEN") }

// Authenticate validates the existing API token without interactive login.
func (p Vercel) Authenticate(_ context.Context, _ string, ci bool, _ io.Reader, _, _ io.Writer) (map[string]string, error) {
	return nil, p.EnsureEnvironment(ci)
}

// Private helpers ------------------------------------------------------------

var vercelSecretKeys = []string{"VERCEL_API_TOKEN", "VERCEL_TOKEN"}
