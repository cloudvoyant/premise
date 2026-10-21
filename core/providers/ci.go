package providers

import (
	"fmt"
	"io"
)

// CIOutput formats named command outputs for a CI runner.
type CIOutput interface {
	WriteOutputs(io.Writer, map[string]any) error
}

// Public API -----------------------------------------------------------------

// CIOutputProvider selects the runner adapter declared by the workspace.
func CIOutputProvider(name string) (CIOutput, error) {
	switch name {
	case "github":
		return GitHub{}, nil
	default:
		return nil, fmt.Errorf("CI output provider %q is not supported", name)
	}
}
