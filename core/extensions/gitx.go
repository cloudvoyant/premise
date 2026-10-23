package extensions

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Public API -----------------------------------------------------------------

// CurrentFeatureBranch returns the checkout's branch for preview environments.
// Detached HEAD and the main or master branches require an explicit branch.
func CurrentFeatureBranch(ctx context.Context, root string) (string, error) {
	command := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("preview requires --branch outside a feature-branch checkout")
	}
	branch := strings.TrimSpace(string(output))
	if branch == "main" || branch == "master" {
		return "", fmt.Errorf("preview requires a feature branch")
	}
	return branch, nil
}

// ExportRevision writes committed files into an existing temporary directory.
// Ignored and untracked checkout files cannot enter this snapshot.
func ExportRevision(ctx context.Context, root, revision, destination string) error {
	command := exec.CommandContext(ctx, "git", "archive", "--format=tar", revision)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("export Git revision %s: %w", revision, err)
	}
	reader := tar.NewReader(bytes.NewReader(output))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(header.Name) {
			return fmt.Errorf("Git archive entry escapes snapshot: %s", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(header.Name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		}
	}
}

// RequireCleanRepository checks that publication uses committed source files.
func RequireCleanRepository(ctx context.Context, root string) error {
	command := exec.CommandContext(ctx, "git", "status", "--porcelain")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("inspect package checkout: %w", err)
	}
	if len(output) > 0 {
		return fmt.Errorf("repository packages require a clean committed checkout before publication")
	}
	return nil
}
