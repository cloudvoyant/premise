// Package cargo isolates Cargo registry detection and package publication.
package cargo

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	misecmd "github.com/cloudvoyant/premise/internal/mise"
)

// IsRegistry reports whether root follows Premise's Cargo registry convention.
func IsRegistry(root string) (bool, error) {
	path := filepath.Join(root, "templates", "Cargo.toml")
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().IsRegular(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Publish invokes the registry's root publish task for one stable version.
func Publish(ctx context.Context, root, version string, stdout, stderr io.Writer) error {
	runner := misecmd.Runner{
		Stdout:            stdout,
		Stderr:            stderr,
		RemoveEnvironment: []string{"GITHUB_TOKEN", "GH_TOKEN", "CRATES_TOKEN"},
	}
	if err := runner.Run(ctx, root, []string{"RELEASE_VERSION=" + version}, "run", "publish"); err != nil {
		return fmt.Errorf("publish Cargo packages: %w", err)
	}
	return nil
}
