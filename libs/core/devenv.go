package core

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
)

// InstallDevTools installs Mise tools declared by the workspace and generated projects.
func InstallDevTools(ctx context.Context, root string, stdout, stderr io.Writer) error {
	runner := miseRunner{Stdout: stdout, Stderr: stderr, Ceiling: filepath.Dir(root)}
	if err := runner.run(ctx, root, nil, "install"); err != nil {
		return fmt.Errorf("mise install: %w", err)
	}
	hasProjectConfigs, err := HasProjectMiseConfigs(root)
	if err != nil {
		return err
	}
	if !hasProjectConfigs {
		fmt.Fprintln(stdout, "No project mise.toml files found; installed the active workspace and global mise tools.")
		return nil
	}
	if err := runner.run(ctx, root, nil, "install", "--monorepo"); err != nil {
		return fmt.Errorf("mise install: %w", err)
	}
	return nil
}
