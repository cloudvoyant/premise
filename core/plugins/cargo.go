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

	"github.com/cloudvoyant/premise/core"
)

type Cargo struct{}

func (Cargo) ID() string        { return "cargo" }
func (Cargo) Ecosystem() string { return "cargo" }

// GetPackageMetadata reads one direct Cargo.toml. Virtual workspaces and
// templates without a direct package are not handled by Cargo.
func (Cargo) GetPackageMetadata(root string, template core.Template) (core.PackageMetadata, bool, error) {
	directory, err := core.TemplateDirectory(root, template.Path)
	if err != nil {
		return core.PackageMetadata{}, false, fmt.Errorf("resolve Cargo template %s: %w", template.Name, err)
	}
	pkg, found, err := core.ReadCargoPackage(filepath.Join(directory, "Cargo.toml"))
	if err != nil || !found {
		return core.PackageMetadata{}, found, err
	}
	if pkg.Name != template.Name {
		return core.PackageMetadata{}, false, fmt.Errorf("Cargo package %q does not match declared template %q", pkg.Name, template.Name)
	}
	return core.PackageMetadata{
		Name:           pkg.Name,
		Version:        pkg.Version,
		Path:           directory,
		PackageManager: "cargo",
		Ecosystem:      "cargo",
		Registry:       "https://crates.io",
		Public:         pkg.PublishCratesIO,
		Publishable:    pkg.PublishCratesIO,
	}, true, nil
}

func (p Cargo) IsPublic(root string, template core.Template) (bool, error) {
	metadata, found, err := p.GetPackageMetadata(root, template)
	return found && metadata.Public, err
}
func (p Cargo) ShouldPublishPackage(root string, template core.Template) (bool, error) {
	metadata, found, err := p.GetPackageMetadata(root, template)
	return found && metadata.Publishable, err
}

// ValidatePackage checks a template's direct Cargo manifest and declared name.
// A template without a direct package is allowed and will be skipped.
func (p Cargo) ValidatePackage(root string, template core.Template) error {
	_, _, err := p.GetPackageMetadata(root, template)
	return err
}

// WillPublishOk checks eligibility, the Mise task, and crates.io availability
// without editing files or publishing. Shared credentials are checked by PublishPackages.
func (Cargo) WillPublishOk(ctx context.Context, root string, template core.Template, version, task string) (bool, error) {
	if _, err := core.ValidatePackagePublicationVersion(version, task); err != nil {
		return false, err
	}
	pkg, found, err := inspectCargoTemplatePackage(root, template)
	if err != nil || !found || !pkg.RegistryPublish {
		return false, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	exists, err := preflightCargoPackage(ctx, pkg, strings.TrimPrefix(version, "v"), task, core.MiseTaskRunner{}, client)
	return !exists && err == nil, err
}
func (Cargo) SupportsPackages() bool { return true }
func (Cargo) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	return publishCargoPackages(ctx, root, version, task, stdout, stderr)
}
func (Cargo) ReleaseWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
	return prepareCargoReleaseWorkspace(ctx, root, stdout, stderr)
}
func (Cargo) CreateGoReleaserConfig(root string, manifest core.Config) (string, error) {
	return cargoReleaseBuilds(root, manifest)
}

type cargoTemplatePackage struct {
	Template        core.Template
	Directory       string
	Name            string
	RegistryPublish bool
}

// cargoWorkspaceDirectory resolves the aggregate Cargo workspace at the
// registry repository root. Template source directories are independent.
func cargoWorkspaceDirectory(root string) (string, bool, error) {
	path := filepath.Join(root, "Cargo.toml")
	found, err := core.IsRegularFile(path)
	if err != nil || !found {
		return "", false, err
	}
	return root, true, nil
}

func inspectCargoTemplatePackage(root string, template core.Template) (cargoTemplatePackage, bool, error) {
	metadata, found, err := (Cargo{}).GetPackageMetadata(root, template)
	if err != nil || !found {
		return cargoTemplatePackage{}, found, err
	}
	return cargoTemplatePackage{
		Template:        template,
		Directory:       metadata.Path,
		Name:            metadata.Name,
		RegistryPublish: metadata.Publishable,
	}, true, nil
}

func cargoReleaseBuilds(root string, manifest core.Config) (string, error) {
	applications := []string{}
	for _, template := range manifest.DeclaredTemplates() {
		if template.Kind != "app" {
			continue
		}
		pkg, found, err := inspectCargoTemplatePackage(root, template)
		if err != nil {
			return "", err
		}
		if found {
			applications = append(applications, pkg.Name)
		}
	}
	if len(applications) == 0 {
		return "", nil // Cargo libraries may publish without downloadable app artifacts.
	}
	var builder strings.Builder
	builder.WriteString("builds:\n")
	for _, application := range applications {
		fmt.Fprintf(&builder, `  - id: %s
    builder: rust
    binary: %s
    dir: .
    targets:
      - x86_64-unknown-linux-gnu
      - aarch64-unknown-linux-gnu
      - x86_64-apple-darwin
      - aarch64-apple-darwin
    flags:
      - --release
      - -p=%s

`, application, application, application)
	}
	builder.WriteString("archives:\n")
	for _, application := range applications {
		fmt.Fprintf(&builder, `  - id: %s
    ids:
      - %s
    formats:
      - tar.gz
    name_template: "{{ .Binary }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}"

`, application, application)
	}
	return builder.String(), nil
}

var cratesAPIBaseURL = "https://crates.io/api/v1"

type cargoFileBackup struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

type cargoPublishTarget struct {
	directory string
	name      string
	exists    bool
}

func preflightCargoPackage(ctx context.Context, pkg cargoTemplatePackage, version, task string, runner core.MiseTaskRunner, client *http.Client) (bool, error) {
	var failures []error
	taskExists, err := runner.TaskExists(ctx, pkg.Directory, task)
	if err != nil {
		failures = append(failures, fmt.Errorf("inspect Cargo package %s task %s: %w", pkg.Name, task, err))
	} else if !taskExists {
		failures = append(failures, fmt.Errorf("Cargo package %s does not define task %s", pkg.Name, task))
	}
	versionExists, err := cargoVersionExists(ctx, client, pkg.Name, version)
	if err != nil {
		failures = append(failures, err)
	}
	return versionExists, errors.Join(failures...)
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
	version, err := core.ValidatePackagePublicationVersion(version, task)
	if err != nil {
		return err
	}
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		return fmt.Errorf("load Cargo registry manifest: %w", err)
	}
	templates := manifest.DeclaredTemplates()
	if len(templates) == 0 {
		return errors.New("Cargo registry manifest declares no templates")
	}

	var failures []error
	packages := make([]cargoTemplatePackage, 0, len(templates))
	for _, template := range templates {
		pkg, found, err := inspectCargoTemplatePackage(root, template)
		if err != nil {
			failures = append(failures, fmt.Errorf("template %s: %w", template.Name, err))
			continue
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
	if len(packages) == 0 && len(failures) == 0 {
		return nil
	}

	workspaceDirectory, found, err := cargoWorkspaceDirectory(root)
	if err != nil {
		failures = append(failures, fmt.Errorf("inspect Cargo registry workspace: %w", err))
	} else if !found {
		failures = append(failures, errors.New("Cargo registry workspace is missing Cargo.toml"))
	}
	client := &http.Client{Timeout: 30 * time.Second}
	preflight := core.MiseTaskRunner{Stderr: stderr}
	publications := make([]cargoPublishTarget, 0, len(packages))
	pending := false
	for _, pkg := range packages {
		versionExists, err := preflightCargoPackage(ctx, pkg, version, task, preflight, client)
		if err != nil {
			failures = append(failures, fmt.Errorf("template %s: %w", pkg.Template.Name, err))
			continue
		}
		publications = append(publications, cargoPublishTarget{directory: pkg.Directory, name: pkg.Name, exists: versionExists})
		pending = pending || !versionExists
	}
	token := os.Getenv("CRATES_TOKEN")
	if pending && token == "" {
		failures = append(failures, errors.New("CRATES_TOKEN is required for Cargo publication"))
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("Cargo publication preflight: %w", err)
	}
	if !pending {
		for _, publication := range publications {
			fmt.Fprintf(stdout, "skip: %s %s already published\n", publication.name, version)
		}
		return nil
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

	publisher := core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}
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
