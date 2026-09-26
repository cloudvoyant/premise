package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/cloudvoyant/premise/core"
)

var cratesAPIBaseURL = "https://crates.io/api/v1"

type cargoFileBackup struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

type cargoPublication struct {
	directory string
	name      string
	exists    bool
}

func prepareCargoReleaseWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
	workspace, found, err := cargoWorkspaceDirectory(root)
	if err != nil {
		return "", false, fmt.Errorf("inspect Cargo release workspace: %w", err)
	}
	if !found {
		return "", false, errors.New("Cargo release workspace is missing Cargo.toml")
	}
	if err := (core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}).Run(ctx, workspace, nil, "install"); err != nil {
		return "", false, fmt.Errorf("prepare Cargo release toolchain: %w", err)
	}
	return workspace, true, nil
}

func publishCargoPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) (resultErr error) {
	version = strings.TrimPrefix(version, "v")
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		return fmt.Errorf("invalid Cargo release version %q: %w", version, err)
	}
	switch task {
	case "publish:rc":
		if parsed.Prerelease() == "" {
			return fmt.Errorf("Cargo RC publication requires a prerelease version, got %s", version)
		}
	case "publish":
		if parsed.Prerelease() != "" || parsed.Metadata() != "" {
			return fmt.Errorf("Cargo stable publication requires a stable version, got %s", version)
		}
	default:
		return fmt.Errorf("unsupported Cargo publication task %q", task)
	}

	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		return fmt.Errorf("load Cargo registry manifest: %w", err)
	}
	templates := manifest.DeclaredTemplates()
	if len(templates) == 0 {
		return errors.New("Cargo registry manifest declares no templates")
	}

	packages := make([]cargoTemplatePackage, 0, len(templates))
	for _, template := range templates {
		pkg, found, err := inspectCargoTemplatePackage(root, template)
		if err != nil {
			return err
		}
		if !found {
			fmt.Fprintf(stdout, "skip: %s has no direct Cargo package\n", template.Name)
			continue
		}
		if !pkg.RegistryPublish {
			fmt.Fprintf(stdout, "skip: %s Cargo registry publication disabled\n", pkg.Name)
			continue
		}
		packages = append(packages, pkg)
	}
	if len(packages) == 0 {
		return nil
	}

	token := os.Getenv("CRATES_TOKEN")
	if token == "" {
		return errors.New("CRATES_TOKEN is required for Cargo publication")
	}
	workspaceDirectory, found, err := cargoWorkspaceDirectory(root)
	if err != nil {
		return fmt.Errorf("inspect Cargo registry workspace: %w", err)
	}
	if !found {
		return errors.New("Cargo registry workspace is missing Cargo.toml")
	}
	backups := make([]cargoFileBackup, 0, len(packages)+1)
	for _, pkg := range packages {
		backup, err := backupCargoFile(filepath.Join(pkg.Directory, "Cargo.toml"))
		if err != nil {
			return err
		}
		backups = append(backups, backup)
	}
	lockPath := filepath.Join(workspaceDirectory, "Cargo.lock")
	lockBackup, err := backupCargoFile(lockPath)
	if err != nil {
		return err
	}
	backups = append(backups, lockBackup)
	defer func() {
		resultErr = errors.Join(resultErr, restoreCargoFiles(backups))
	}()

	for _, backup := range backups[:len(packages)] {
		if err := core.SetCargoPackageVersion(backup.path, version); err != nil {
			return err
		}
	}
	lockRunner := core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}
	if err := lockRunner.Run(ctx, workspaceDirectory, nil, "exec", "--", "cargo", "generate-lockfile"); err != nil {
		return fmt.Errorf("regenerate Cargo lockfile: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	preflight := core.MiseTaskRunner{Stderr: stderr}
	publisher := core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}
	publications := make([]cargoPublication, 0, len(packages))
	for _, pkg := range packages {
		taskExists, err := preflight.TaskExists(ctx, pkg.Directory, task)
		if err != nil {
			return fmt.Errorf("inspect Cargo package %s task %s: %w", pkg.Name, task, err)
		}
		if !taskExists {
			return fmt.Errorf("Cargo package %s does not define task %s", pkg.Name, task)
		}
		versionExists, err := cargoVersionExists(ctx, client, pkg.Name, version)
		if err != nil {
			return err
		}
		publications = append(publications, cargoPublication{directory: pkg.Directory, name: pkg.Name, exists: versionExists})
	}
	for _, publication := range publications {
		if publication.exists {
			fmt.Fprintf(stdout, "skip: %s %s already published\n", publication.name, version)
			continue
		}
		fmt.Fprintf(stdout, "%s: %s %s\n", task, publication.name, version)
		environment := []string{
			"RELEASE_VERSION=" + version,
			"CARGO_REGISTRY_TOKEN=" + token,
		}
		if err := publisher.Run(ctx, publication.directory, environment, "run", task); err != nil {
			return fmt.Errorf("publish Cargo package %s: %w", publication.name, err)
		}
	}
	return nil
}

func backupCargoFile(path string) (cargoFileBackup, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cargoFileBackup{path: path}, nil
	}
	if err != nil {
		return cargoFileBackup{}, fmt.Errorf("back up Cargo file %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return cargoFileBackup{}, fmt.Errorf("inspect Cargo file %s: %w", path, err)
	}
	return cargoFileBackup{path: path, data: data, mode: info.Mode().Perm(), exists: true}, nil
}

func restoreCargoFiles(backups []cargoFileBackup) error {
	var failures []error
	for _, backup := range backups {
		if !backup.exists {
			if err := os.Remove(backup.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("remove generated Cargo file %s: %w", backup.path, err))
			}
			continue
		}
		if err := os.WriteFile(backup.path, backup.data, backup.mode); err != nil {
			failures = append(failures, fmt.Errorf("restore Cargo file %s: %w", backup.path, err))
		}
	}
	return errors.Join(failures...)
}

func cargoVersionExists(ctx context.Context, client *http.Client, name, version string) (bool, error) {
	return cargoResourceExists(ctx, client, cratesAPIBaseURL+"/crates/"+name+"/"+version, "crate version "+name+" "+version)
}

func cargoResourceExists(ctx context.Context, client *http.Client, resourceURL, description string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", "premise-release-check")
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("check crates.io %s: %w", description, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("check crates.io %s: HTTP %d", description, response.StatusCode)
	}
}
