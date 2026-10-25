package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/cloudvoyant/premise/core"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Public API -----------------------------------------------------------------

// Cargo implements crates.io publication and Rust artifact preparation.
type Cargo struct{}

// ID returns the package manager identifier used by premise.yaml.
func (Cargo) ID() string { return "cargo" }

// Ecosystem returns the Cargo package ecosystem identifier.
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

// IsPackagePublic reports whether a template has a public Cargo package.
func (p Cargo) IsPackagePublic(root string, template core.Template) (bool, error) {
	metadata, found, err := p.GetPackageMetadata(root, template)
	return found && metadata.Public, err
}

// ShouldPublishPackage reports whether a template has an eligible Cargo package.
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

// PreflightPublication validates every Cargo target before stable tagging.
func (Cargo) PreflightPublication(ctx context.Context, root, version, task string) error {
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		return fmt.Errorf("load Cargo registry manifest: %w", err)
	}
	if len(manifest.PublicationTargets()) == 0 {
		return errors.New("Cargo manifest declares no publication targets")
	}
	var failures []error
	pending := false
	workspace, found, workspaceErr := cargoWorkspaceDirectory(root)
	if workspaceErr != nil {
		failures = append(failures, fmt.Errorf("inspect Cargo registry workspace: %w", workspaceErr))
	} else if !found || workspace == "" {
		failures = append(failures, errors.New("Cargo registry workspace is missing Cargo.toml"))
	}
	for _, target := range manifest.PublicationTargets() {
		metadata, found, metadataErr := (Cargo{}).GetPackageMetadata(root, target)
		if metadataErr != nil {
			failures = append(failures, fmt.Errorf("package target %s: %w", target.Name, metadataErr))
			continue
		}
		if !found || !metadata.Publishable {
			continue
		}
		ready, checkErr := (Cargo{}).WillPublishOk(ctx, root, target, version, task)
		if checkErr != nil {
			failures = append(failures, fmt.Errorf("package target %s: %w", target.Name, checkErr))
			continue
		}
		if ready {
			pending = true
		}
	}
	if pending && os.Getenv("CRATES_TOKEN") == "" {
		failures = append(failures, errors.New("CRATES_TOKEN is required for Cargo publication"))
	}
	return errors.Join(failures...)
}

// SupportsPackages reports that Cargo publishes registry packages.
func (Cargo) SupportsPackages() bool { return true }

// PublishPackages publishes every eligible Cargo package after a complete preflight.
func (Cargo) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	return publishCargoPackages(ctx, root, version, task, stdout, stderr)
}

// ReleaseWorkspace prepares the aggregate Cargo workspace for artifact builds.
func (Cargo) ReleaseWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
	return prepareCargoReleaseWorkspace(ctx, root, stdout, stderr)
}

// CreateGoReleaserConfig returns Rust build and archive definitions.
func (Cargo) CreateGoReleaserConfig(root string, manifest core.Config) (string, error) {
	return cargoReleaseBuilds(root, manifest)
}

// ParseDependencies reads normal, workspace, and target-specific Cargo tables.
// Version requirements and native settings remain separate in the common model.
func (Cargo) ParseDependencies(manifest []byte) ([]core.Dependency, error) {
	var document map[string]any
	if err := toml.Unmarshal(manifest, &document); err != nil {
		return nil, err
	}
	tables, err := cargoDependencyTables(document)
	if err != nil {
		return nil, err
	}
	var dependencies []core.Dependency
	for _, table := range tables {
		for name, raw := range table.values {
			dependency := core.Dependency{Name: name, Scope: table.scope}
			switch value := raw.(type) {
			case string:
				dependency.Version = value
			case map[string]any:
				options := maps.Clone(value)
				dependency.Options = options
				if version, present := value["version"]; present {
					var valid bool
					dependency.Version, valid = version.(string)
					if !valid {
						return nil, fmt.Errorf("Cargo dependency %s version must be a string", name)
					}
					delete(options, "version")
				}
			default:
				return nil, fmt.Errorf("Cargo dependency %s must be a version or table", name)
			}
			dependencies = append(dependencies, dependency)
		}
	}
	sort.Slice(dependencies, func(i, j int) bool {
		return strings.Join(dependencies[i].Scope, ".")+"."+dependencies[i].Name < strings.Join(dependencies[j].Scope, ".")+"."+dependencies[j].Name
	})
	return dependencies, nil
}

// WriteDependencies replaces Cargo dependency tables and retains all other values.
// An unchanged dependency list returns the original bytes, including comments.
func (Cargo) WriteDependencies(manifest []byte, dependencies []core.Dependency) ([]byte, error) {
	existing, err := (Cargo{}).ParseDependencies(manifest)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 && len(dependencies) == 0 || reflect.DeepEqual(existing, dependencies) {
		return append([]byte(nil), manifest...), nil
	}
	document := map[string]any{}
	if err := toml.Unmarshal(manifest, &document); err != nil {
		return nil, err
	}
	tables, err := cargoDependencyTables(document)
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		clear(table.values)
	}
	for _, dependency := range dependencies {
		if !validCargoDependencyScope(dependency.Scope) {
			return nil, fmt.Errorf("invalid Cargo dependency scope %v", dependency.Scope)
		}
		table := document
		for _, key := range dependency.Scope {
			next, ok := table[key].(map[string]any)
			if !ok {
				next = map[string]any{}
				table[key] = next
			}
			table = next
		}
		if dependency.Options == nil {
			table[dependency.Name] = dependency.Version
			continue
		}
		nativeOptions, ok := dependency.Options.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid Cargo dependency options for %s", dependency.Name)
		}
		options := maps.Clone(nativeOptions)
		if dependency.Version != "" {
			options["version"] = dependency.Version
		}
		table[dependency.Name] = options
	}
	return toml.Marshal(document)
}

func (Cargo) GenerationRootFiles() []string { return []string{"Cargo.toml", "Cargo.lock"} }

func (Cargo) PrepareGeneration(request core.PackageGenerationContext) ([]core.MergeEntry, error) {
	selected, found, err := readCargoGenerationCrate(request, request.ProjectPath, request.Stage)
	if err != nil || !found {
		return nil, err
	}
	members, covered, err := collectCargoWorkspaceMembers(request)
	if err != nil {
		return nil, err
	}
	original := request.ClientFiles["Cargo.toml"]
	output := original.Data
	if !covered {
		members = append(members, filepath.ToSlash(request.ProjectPath))
		output, err = writeCargoWorkspaceMembers(original.Data, members)
		if err != nil {
			return nil, err
		}
	}
	if !original.Present {
		resolver := "2"
		pkg, _ := selected["package"].(map[string]any)
		if pkg["edition"] == "2024" {
			resolver = "3"
		}
		output = append(output, []byte("resolver = \""+resolver+"\"\n")...)
	}
	output, err = core.MergeManifestDependencies(Cargo{}, "Cargo.toml", request.SharedFiles["Cargo.toml"].Data, output, request.ResolveConflict)
	if err != nil {
		return nil, err
	}
	var verified map[string]any
	if err := toml.Unmarshal(output, &verified); err != nil {
		return nil, fmt.Errorf("cannot extend Cargo workspace without changing its layout: %w", err)
	}
	if err := validateCargoGenerationManifest(request, "", verified); err != nil {
		return nil, err
	}
	return request.ResetLocks([]core.MergeEntry{request.RootEntry("Cargo.toml", output)}, "Cargo.lock")
}

// Private helpers ------------------------------------------------------------

type cargoDependencyTable struct {
	scope  []string
	values map[string]any
}

func cargoDependencyTables(document map[string]any) ([]cargoDependencyTable, error) {
	var scopes [][]string
	for _, section := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
		scopes = append(scopes, []string{section})
	}
	scopes = append(scopes, []string{"workspace", "dependencies"})
	if targets, ok := document["target"].(map[string]any); ok {
		for target := range targets {
			for _, section := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
				scopes = append(scopes, []string{"target", target, section})
			}
		}
	}
	var tables []cargoDependencyTable
	for _, scope := range scopes {
		table := document
		for _, key := range scope[:len(scope)-1] {
			table, _ = table[key].(map[string]any)
		}
		key := scope[len(scope)-1]
		if raw, present := table[key]; present {
			values, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("Cargo %s must be a table", strings.Join(scope, "."))
			}
			tables = append(tables, cargoDependencyTable{scope: scope, values: values})
		}
	}
	return tables, nil
}

func validCargoDependencyScope(scope []string) bool {
	if len(scope) == 0 {
		return false
	}
	section := scope[len(scope)-1]
	if section != "dependencies" && section != "dev-dependencies" && section != "build-dependencies" {
		return false
	}
	return len(scope) == 1 || len(scope) == 2 && scope[0] == "workspace" && section == "dependencies" || len(scope) == 3 && scope[0] == "target"
}

func readCargoGenerationCrate(request core.PackageGenerationContext, base, directory string) (map[string]any, bool, error) {
	data, err := os.ReadFile(filepath.Join(directory, "Cargo.toml"))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var crate map[string]any
	if err := toml.Unmarshal(data, &crate); err != nil {
		return nil, false, fmt.Errorf("crate Cargo.toml: %w", err)
	}
	if _, found := crate["package"]; !found {
		return nil, false, nil
	}
	if _, found := crate["workspace"]; found {
		return nil, false, fmt.Errorf("crate Cargo.toml defines a nested workspace; templates must provide a crate that joins the client workspace")
	}
	if err := validateCargoGenerationManifest(request, base, crate); err != nil {
		return nil, false, err
	}
	return crate, true, nil
}

func collectCargoWorkspaceMembers(request core.PackageGenerationContext) ([]string, bool, error) {
	var client struct {
		Workspace *struct {
			Members []string `toml:"members"`
			Exclude []string `toml:"exclude"`
		} `toml:"workspace"`
	}
	if err := toml.Unmarshal(request.ClientFiles["Cargo.toml"].Data, &client); err != nil {
		return nil, false, fmt.Errorf("client Cargo.toml: %w", err)
	}
	var clientValues map[string]any
	if err := toml.Unmarshal(request.ClientFiles["Cargo.toml"].Data, &clientValues); err != nil {
		return nil, false, err
	}
	if err := validateCargoGenerationManifest(request, "", clientValues); err != nil {
		return nil, false, err
	}
	members := []string{}
	if client.Workspace != nil {
		for _, pattern := range client.Workspace.Exclude {
			if err := request.ValidateLocalPath("", pattern); err != nil {
				return nil, false, err
			}
			excluded, err := request.MatchesProject(pattern)
			if err != nil {
				return nil, false, err
			}
			if excluded {
				return nil, false, fmt.Errorf("Cargo.toml workspace.exclude excludes generated crate %s with %q", request.ProjectPath, pattern)
			}
		}
		members = client.Workspace.Members
		alreadyCovered := false
		for _, pattern := range members {
			if err := request.ValidateLocalPath("", pattern); err != nil {
				return nil, false, err
			}
			covered, err := request.MatchesProject(pattern)
			if err != nil {
				return nil, false, err
			}
			alreadyCovered = alreadyCovered || covered
		}
		if alreadyCovered {
			return members, true, nil
		}
	}
	if client.Workspace == nil {
		return readExistingCargoWorkspaceMembers(request)
	}
	return members, false, nil
}

func readExistingCargoWorkspaceMembers(request core.PackageGenerationContext) ([]string, bool, error) {
	members := []string{}
	for _, path := range request.ProjectPaths {
		if err := request.ValidateLocalPath("", path); err != nil {
			return nil, false, err
		}
		_, found, err := readCargoGenerationCrate(request, path, filepath.Join(request.WorkspaceRoot, path))
		if err != nil {
			return nil, false, fmt.Errorf("existing crate %s: %w", path, err)
		}
		if found {
			members = append(members, filepath.ToSlash(path))
		}
	}
	return members, false, nil
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
			return "", fmt.Errorf("inspect Cargo template %q: %w", template.Name, err)
		}
		if !found {
			continue
		}
		tauriConfig := filepath.Join(pkg.Directory, "tauri.conf.json")
		isTauri, err := core.IsRegularFile(tauriConfig)
		if err != nil {
			return "", fmt.Errorf("inspect Tauri config for template %q: %w", template.Name, err)
		}
		if !isTauri {
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
	templates := manifest.PublicationTargets()
	if len(templates) == 0 {
		return errors.New("Cargo manifest declares no publication targets")
	}

	var failures []error
	packages := make([]cargoTemplatePackage, 0, len(templates))
	for _, template := range templates {
		pkg, found, err := inspectCargoTemplatePackage(root, template)
		if err != nil {
			failures = append(failures, fmt.Errorf("package target %s: %w", template.Name, err))
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
			failures = append(failures, fmt.Errorf("package target %s: %w", pkg.Template.Name, err))
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

// Replace only workspace.members, leaving every other source byte untouched.
func writeCargoWorkspaceMembers(data []byte, members []string) ([]byte, error) {
	encoded, err := json.Marshal(members)
	if err != nil {
		return nil, err
	}
	var parser unstable.Parser
	parser.Reset(data)
	section := ""
	tableEnd := -1
	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table, unstable.ArrayTable:
			section = cargoTOMLKey(expression)
			if section == "workspace" && expression.Kind == unstable.Table {
				first := expression.Child()
				start := int(first.Raw.Offset + first.Raw.Length)
				newline := bytes.IndexByte(data[start:], '\n')
				tableEnd = len(data)
				if newline >= 0 {
					tableEnd = start + newline + 1
				}
			}
		case unstable.KeyValue:
			key := cargoTOMLKey(expression)
			if section != "" {
				key = section + "." + key
			}
			if key != "workspace.members" {
				continue
			}
			value := expression.Value()
			if value.Kind != unstable.Array {
				return nil, fmt.Errorf("Cargo workspace.members must be an array")
			}
			keys := expression.Key()
			keyEnd := 0
			for keys.Next() {
				node := keys.Node()
				keyEnd = int(node.Raw.Offset + node.Raw.Length)
			}
			open := bytes.IndexByte(data[keyEnd:], '[')
			if open < 0 {
				return nil, fmt.Errorf("cannot locate Cargo workspace.members array")
			}
			open += keyEnd
			end := open + 1
			children := value.Children()
			for children.Next() {
				child := children.Node()
				position := int(child.Raw.Offset + child.Raw.Length)
				if position > end {
					end = position
				}
			}
			for end < len(data) {
				if data[end] == '#' {
					for end < len(data) && data[end] != '\n' {
						end++
					}
				} else if data[end] == ']' {
					output := append([]byte(nil), data[:open]...)
					output = append(output, encoded...)
					return append(output, data[end+1:]...), nil
				} else {
					end++
				}
			}
			return nil, fmt.Errorf("cannot locate end of Cargo workspace.members")
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	line := append([]byte("members = "), encoded...)
	line = append(line, '\n')
	if tableEnd >= 0 {
		output := append([]byte(nil), data[:tableEnd]...)
		if len(output) > 0 && output[len(output)-1] != '\n' {
			output = append(output, '\n')
		}
		output = append(output, line...)
		return append(output, data[tableEnd:]...), nil
	}
	output := append([]byte(nil), data...)
	if len(output) > 0 && output[len(output)-1] != '\n' {
		output = append(output, '\n')
	}
	output = append(output, []byte("\n[workspace]\n")...)
	return append(output, line...), nil
}

func cargoTOMLKey(node *unstable.Node) string {
	keys := node.Key()
	var parts []string
	for keys.Next() {
		parts = append(parts, string(keys.Node().Data))
	}
	return strings.Join(parts, ".")
}

func validateCargoGenerationManifest(request core.PackageGenerationContext, base string, manifest map[string]any) error {
	pkg, _ := manifest["package"].(map[string]any)
	if workspace, ok := pkg["workspace"].(string); ok {
		if err := request.ValidateLocalPath(base, workspace); err != nil {
			return err
		}
		if filepath.Clean(filepath.Join(base, workspace)) != "." {
			return fmt.Errorf("Cargo package.workspace must point to the client workspace root")
		}
	}
	if build, ok := pkg["build"].(string); ok {
		if err := request.ValidateLocalPath(base, build); err != nil {
			return err
		}
	}
	// Only native dependency/source tables contain filesystem references; metadata is opaque.
	for _, key := range []string{"dependencies", "dev-dependencies", "build-dependencies", "patch", "replace", "lib", "bin", "test", "bench", "example"} {
		if err := validateCargoGenerationPaths(request, base, manifest[key]); err != nil {
			return err
		}
	}
	if workspace, ok := manifest["workspace"].(map[string]any); ok {
		if err := validateCargoGenerationPaths(request, "", workspace["dependencies"]); err != nil {
			return err
		}
	}
	if targets, ok := manifest["target"].(map[string]any); ok {
		for _, target := range targets {
			if object, ok := target.(map[string]any); ok {
				for _, key := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
					if err := validateCargoGenerationPaths(request, base, object[key]); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func validateCargoGenerationPaths(request core.PackageGenerationContext, base string, value any) error {
	switch object := value.(type) {
	case map[string]any:
		if path, ok := object["path"].(string); ok {
			if err := request.ValidateLocalPath(base, path); err != nil {
				return err
			}
		}
		for _, child := range object {
			if err := validateCargoGenerationPaths(request, base, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range object {
			if err := validateCargoGenerationPaths(request, base, child); err != nil {
				return err
			}
		}
	}
	return nil
}
