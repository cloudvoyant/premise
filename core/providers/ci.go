package providers

import (
	"context"
	"fmt"
)

// CIPlan contains scheduling values before the runner chooses its output format.
// Matrix and Migrations are scheduler-owned slices; the adapter owns their shape.
type CIPlan struct {
	Mode              string
	Runner            string
	HasNative         bool
	HasInfrastructure bool
	Matrix            any
	Migrations        any
	HasMigrations     bool
}

// Public API -----------------------------------------------------------------

// CIOutputProvider selects the runner adapter declared by the workspace.
func CIOutputProvider(name string) (CIProvider, error) {
	switch name {
	case "github":
		return GitHub{}, nil
	default:
		return nil, fmt.Errorf("CI output provider %q is not supported", name)
	}
}

// CIIdentityToken delegates the native GitHub runner identity exchange.
// Other runners supply CI_OIDC_TOKEN_FILE before cloud authentication.
func CIIdentityToken(ctx context.Context, audience, path string) error {
	provider, err := CIOutputProvider("github")
	if err != nil {
		return err
	}
	return provider.IdentityToken(ctx, audience, path)
}
