package backends

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"github.com/gobwas/glob"
)

// Private helpers ------------------------------------------------------------

// Core attaches original client values and preserves their modes during planning.
func generationEntry(path string, data []byte) core.MergeEntry {
	return core.MergeEntry{Path: path, Output: core.PathValue{Present: true, Kind: "file", Mode: 0644, Data: data}, Strategy: core.MergeStrategyCopy}
}

func generationPatternMatches(pattern, path string) (bool, error) {
	matcher, err := glob.Compile(strings.TrimPrefix(pattern, "./"), '/')
	if err != nil {
		return false, fmt.Errorf("invalid workspace pattern %q: %w", pattern, err)
	}
	return matcher.Match(filepath.ToSlash(path)), nil
}

func validateGenerationLocalPath(root, base, path string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute local dependency path %q is unsupported; use a workspace-relative path so candidate validation cannot read the live workspace", path)
	}
	absolute := filepath.Join(root, base, filepath.FromSlash(path))
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("local dependency path %q escapes workspace; generation supports local sources within the workspace root", path)
	}
	return nil
}
