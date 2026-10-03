package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RunPlatformFlow builds artifacts for one declared project template.
func RunPlatformFlow(ctx context.Context, root, project, channel, version, outputDir string, releaseMode CIReleaseMode, stdout, stderr io.Writer) error {
	if releaseMode != CIReleaseNone {
		return fmt.Errorf("on-platform only supports --release none")
	}
	if project == "" {
		return fmt.Errorf("project is required")
	}
	if channel != "stable" && channel != "rc" {
		return fmt.Errorf("invalid channel %q: expected stable or rc", channel)
	}
	task := "publish"
	if channel == "rc" {
		task = "publish:rc"
	}
	normalized, err := ValidatePackagePublicationVersion(version, task)
	if err != nil {
		return err
	}
	if outputDir == "" || !filepath.IsAbs(outputDir) {
		return fmt.Errorf("output-dir must be an absolute path")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("output-dir must be empty before a platform build")
	}

	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	if manifest.TemplateRegistry == nil {
		return fmt.Errorf("manifest has no template registry")
	}
	var selected *Template
	for i := range manifest.TemplateRegistry.Templates {
		if manifest.TemplateRegistry.Templates[i].Name == project {
			selected = &manifest.TemplateRegistry.Templates[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("unknown project %q", project)
	}
	directory, err := TemplateDirectory(root, selected.Path)
	if err != nil {
		return err
	}
	runner := miseRunner{Stdout: stdout, Stderr: stderr, Ceiling: filepath.Dir(filepath.Clean(root))}
	exists, err := runner.taskExists(ctx, directory, "release:build")
	if err != nil {
		return fmt.Errorf("inspect release:build task: %w", err)
	}
	if !exists {
		return fmt.Errorf("template %q has no release:build task", project)
	}
	if err := runner.run(ctx, directory, nil, "install"); err != nil {
		return fmt.Errorf("mise install: %w", err)
	}
	// The project task owns its dependencies (including install), so its
	// chosen build system runs setup only once per platform.
	env := []string{"RELEASE_VERSION=" + normalized, "PREMISE_RELEASE_CHANNEL=" + channel, "PREMISE_ARTIFACT_DIR=" + outputDir}
	if err := runner.run(ctx, directory, env, "run", "release:build"); err != nil {
		return fmt.Errorf("run release:build: %w", err)
	}
	nonempty := false
	err = filepath.Walk(outputDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("invalid or empty platform artifact: %s", path)
		}
		nonempty = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect artifacts: %w", err)
	}
	if !nonempty {
		return fmt.Errorf("release:build produced no nonempty regular artifacts in %s", outputDir)
	}
	return nil
}
