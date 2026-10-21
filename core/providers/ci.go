package providers

import (
	"fmt"
	"io"
)

// CIPlan contains scheduling values before the runner chooses its output format.
// Matrix and Migrations are scheduler-owned slices; the adapter owns their shape.
type CIPlan struct {
	Mode          string
	Runner        string
	HasNative     bool
	Matrix        any
	Migrations    any
	HasMigrations bool
}

// CIPlanOutput formats a scheduling plan for a CI runner.
type CIPlanOutput interface {
	WritePlan(io.Writer, CIPlan) error
}

// Public API -----------------------------------------------------------------

// CIOutputProvider selects the runner adapter declared by the workspace.
func CIOutputProvider(name string) (CIPlanOutput, error) {
	switch name {
	case "github":
		return GitHub{}, nil
	default:
		return nil, fmt.Errorf("CI output provider %q is not supported", name)
	}
}
