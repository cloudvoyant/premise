package core

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PreparePlatformConfig writes a Tauri-compatible version override. The caller's
// Mise task owns the build command; Premise only supplies the release version.
func PreparePlatformConfig(root, mode string) error {
	version, _, err := platformFileVersion(mode)
	if err != nil {
		return err
	}
	config, err := json.Marshal(map[string]string{"version": version})
	if err != nil {
		return err
	}
	// Keep compiled dependencies, but discard bundles restored from a prior
	// release. Only newly built installers may enter the file handoff.
	if _, err := os.Stat(filepath.Join(root, "Cargo.toml")); err == nil {
		bundleDir, err := resolvePlatformSource(root, "cargo:release/bundle")
		if err != nil {
			return err
		}
		if err := os.RemoveAll(bundleDir); err != nil {
			return fmt.Errorf("remove stale release bundles: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	path := filepath.Join(root, "target", "premise-release-config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create release config directory: %w", err)
	}
	if err := os.WriteFile(path, config, 0o644); err != nil {
		return fmt.Errorf("write release config: %w", err)
	}
	return nil
}

// CollectPlatformFiles transfers a project's build output without knowing its
// build system. 'build' copies to the artifact handoff; legacy publish modes
// upload to an already-created GitHub Release.
func CollectPlatformFiles(root, mode, source, suffixes string) error {
	version, _, err := platformFileVersion(mode)
	if err != nil {
		return err
	}
	sourceDir, err := resolvePlatformSource(root, source)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for suffix := range strings.SplitSeq(suffixes, ",") {
		if suffix == "" || !strings.HasPrefix(suffix, ".") || strings.ContainsAny(suffix, `/\`) {
			return fmt.Errorf("invalid file suffix %q", suffix)
		}
		allowed[suffix] = true
	}
	output := os.Getenv("PREMISE_ARTIFACT_DIR")
	repository := os.Getenv("GITHUB_REPOSITORY")
	token := os.Getenv("PREMISE_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if mode == "build" {
		if output == "" || !filepath.IsAbs(output) {
			return fmt.Errorf("PREMISE_ARTIFACT_DIR must be an absolute path")
		}
		if err := os.MkdirAll(output, 0o755); err != nil {
			return fmt.Errorf("create artifact directory: %w", err)
		}
	} else if repository == "" || token == "" {
		return fmt.Errorf("GITHUB_REPOSITORY and a GitHub release token are required")
	}

	seen := map[string]bool{}
	var files []string
	err = filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			// Bundle staging directories (such as AppDir) contain their own
			// links. Reject linked installers, not unrelated support files.
			if allowed[filepath.Ext(entry.Name())] {
				return fmt.Errorf("linked artifact path is not allowed: %s", path)
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !allowed[filepath.Ext(entry.Name())] {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() == 0 {
			return fmt.Errorf("empty artifact: %s", path)
		}
		if seen[entry.Name()] {
			return fmt.Errorf("duplicate artifact name %q", entry.Name())
		}
		seen[entry.Name()] = true
		files = append(files, path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect platform files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no matching release files in %s", source)
	}
	for _, path := range files {
		if mode != "build" {
			command := exec.Command("gh", "release", "upload", "v"+version,
				path, "--clobber", "--repo", repository)
			command.Env = append(os.Environ(), "GH_TOKEN="+token)
			if combined, err := command.CombinedOutput(); err != nil {
				return fmt.Errorf("upload artifact %s: %w: %s", path, err, strings.TrimSpace(string(combined)))
			}
			continue
		}
		if err := copyPlatformFile(path, filepath.Join(output, filepath.Base(path))); err != nil {
			return err
		}
	}
	return nil
}

// resolvePlatformSource lets a project use its normal Cargo workspace target
// directory, whether the task runs in a registry or in a generated project.
// Other build systems pass a project-relative output directory unchanged.
func resolvePlatformSource(root, source string) (string, error) {
	cargoTarget := strings.HasPrefix(source, "cargo:")
	if cargoTarget {
		source = strings.TrimPrefix(source, "cargo:")
	}
	clean := filepath.Clean(source)
	if source == "" || filepath.IsAbs(source) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source must be a directory beneath the project or Cargo target")
	}
	if !cargoTarget {
		return filepath.Join(root, clean), nil
	}
	command := exec.Command("cargo", "metadata", "--format-version", "1", "--no-deps")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve Cargo workspace target: %w", err)
	}
	var metadata struct {
		TargetDirectory string `json:"target_directory"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		return "", fmt.Errorf("decode Cargo metadata: %w", err)
	}
	if !filepath.IsAbs(metadata.TargetDirectory) {
		return "", fmt.Errorf("Cargo metadata returned invalid target directory %q", metadata.TargetDirectory)
	}
	return filepath.Join(metadata.TargetDirectory, clean), nil
}

func copyPlatformFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("stage artifact %s: %w", filepath.Base(source), err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(destination)
		return err
	}
	return out.Close()
}

func platformFileVersion(mode string) (string, string, error) {
	channel := os.Getenv("PREMISE_RELEASE_CHANNEL")
	switch mode {
	case "build":
		if channel != "stable" && channel != "rc" {
			return "", "", fmt.Errorf("PREMISE_RELEASE_CHANNEL must be stable or rc")
		}
	case "publish":
		channel = "stable"
	case "publish:rc":
		channel = "rc"
	default:
		return "", "", fmt.Errorf("unsupported platform file mode %q", mode)
	}
	task := "publish"
	if channel == "rc" {
		task = "publish:rc"
	}
	version, err := ValidatePackagePublicationVersion(os.Getenv("RELEASE_VERSION"), task)
	if err != nil {
		return "", "", err
	}
	return version, channel, nil
}
