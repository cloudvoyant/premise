package core

import (
	"errors"
	"fmt"
	"os"
)

// IsRegularFile reports whether path names an ordinary file. Package-manager
// detection uses it to distinguish manifest files from missing paths and
// directories. A missing path returns false without an error; other stat
// failures are returned. Like os.Stat, it follows symbolic links.
func IsRegularFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return info.Mode().IsRegular(), nil
}
