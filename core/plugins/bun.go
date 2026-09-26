package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core"
)

type Bun struct{}

func (Bun) ID() string        { return "bun" }
func (Bun) Ecosystem() string { return "npm" }

// GetPackageMetadata reads one direct package.json without applying release
// sequencing. Templates without a direct package are not handled by Bun.
func (Bun) GetPackageMetadata(root string, template core.Template) (core.PackageMetadata, bool, error) {
	directory, err := core.TemplateDirectory(root, template.Path)
	if err != nil {
		return core.PackageMetadata{}, false, err
	}
	pkg, found, err := core.ReadBunPackage(filepath.Join(directory, "package.json"))
	if err != nil || !found {
		return core.PackageMetadata{}, found, err
	}
	if pkg.Name != template.Name {
		return core.PackageMetadata{}, false, fmt.Errorf("Bun package %q does not match declared template %q", pkg.Name, template.Name)
	}
	registry, err := bunPackageRegistry(pkg)
	if err != nil {
		return core.PackageMetadata{}, false, err
	}
	metadata := core.PackageMetadata{
		Name:           pkg.Name,
		Version:        pkg.Version,
		Path:           directory,
		PackageManager: "bun",
		Ecosystem:      "npm",
		Public:         !pkg.Private && pkg.PublishConfig.Access == "public",
		Publishable:    !pkg.Private && registry != nil,
	}
	if registry != nil {
		metadata.Registry = registry.String()
	}
	return metadata, true, nil
}

func (p Bun) IsPublic(root string, template core.Template) (bool, error) {
	metadata, found, err := p.GetPackageMetadata(root, template)
	return found && metadata.Public, err
}
func (p Bun) ShouldPublishPackage(root string, template core.Template) (bool, error) {
	metadata, found, err := p.GetPackageMetadata(root, template)
	return found && metadata.Publishable, err
}

// ValidatePackage checks a template's Bun manifest and publication metadata.
// A missing direct package or a private template is allowed and will be skipped.
func (p Bun) ValidatePackage(root string, template core.Template) error {
	_, _, err := p.GetPackageMetadata(root, template)
	return err
}

// WillPublishOk checks package eligibility and its Mise task without publishing.
// Shared registry credentials are checked once by PublishPackages, not here.
func (Bun) WillPublishOk(ctx context.Context, root string, template core.Template, version, task string) (bool, error) {
	if _, err := core.ValidatePackagePublicationVersion(version, task); err != nil {
		return false, err
	}
	_, found, err := preflightBunPackage(ctx, root, template, task, core.MiseTaskRunner{})
	return found, err
}
func (Bun) SupportsPackages() bool { return true }
func (Bun) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	return publishBunPackages(ctx, root, version, task, stdout, stderr)
}
func (Bun) ReleaseWorkspace(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
	return root, false, nil
}

// Bun publishes registry packages, not downloadable native binaries.
func (Bun) CreateGoReleaserConfig(_ string, _ core.Config) (string, error) { return "", nil }

func bunPackageRegistry(pkg core.BunPackage) (*url.URL, error) {
	if pkg.Private {
		return nil, nil
	}
	if pkg.PublishConfig.Registry == "" {
		if pkg.PublishConfig.Access != "" {
			return nil, fmt.Errorf("Bun package %s has publication access but no registry", pkg.Name)
		}
		return nil, nil
	}
	registry, err := url.Parse(pkg.PublishConfig.Registry)
	if err != nil || registry.Host == "" || registry.Scheme != "https" || registry.RawQuery != "" || registry.Fragment != "" || registry.User != nil || strings.ContainsAny(registry.Host+registry.Path, "\r\n") {
		return nil, fmt.Errorf("Bun package %s has invalid HTTPS registry %q", pkg.Name, pkg.PublishConfig.Registry)
	}
	return registry, nil
}

func preflightBunPackage(ctx context.Context, root string, template core.Template, task string, runner core.MiseTaskRunner) (bunPublishTarget, bool, error) {
	metadata, found, err := (Bun{}).GetPackageMetadata(root, template)
	if err != nil || !found || !metadata.Publishable {
		return bunPublishTarget{}, false, err
	}
	registry, err := url.Parse(metadata.Registry)
	if err != nil {
		return bunPublishTarget{}, false, fmt.Errorf("parse Bun package %s registry: %w", metadata.Name, err)
	}
	exists, err := runner.TaskExists(ctx, metadata.Path, task)
	if err != nil {
		return bunPublishTarget{}, false, fmt.Errorf("inspect Bun package %s task %s: %w", metadata.Name, task, err)
	}
	if !exists {
		return bunPublishTarget{}, false, fmt.Errorf("Bun package %s does not define task %s", metadata.Name, task)
	}
	return bunPublishTarget{directory: metadata.Path, name: metadata.Name, registry: registry}, true, nil
}

type bunPublishTarget struct {
	directory string
	name      string
	registry  *url.URL
}

func publishBunPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) (resultErr error) {
	version, err := core.ValidatePackagePublicationVersion(version, task)
	if err != nil {
		return err
	}
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		return fmt.Errorf("load Bun registry manifest: %w", err)
	}
	var publications []bunPublishTarget
	var failures []error
	preflight := core.MiseTaskRunner{Stderr: stderr}
	for _, template := range manifest.DeclaredTemplates() {
		publication, ready, err := preflightBunPackage(ctx, root, template, task, preflight)
		if err != nil {
			failures = append(failures, fmt.Errorf("template %s: %w", template.Name, err))
			continue
		}
		if !ready {
			fmt.Fprintf(stdout, "skip: %s Bun registry publication disabled\n", template.Name)
			continue
		}
		publications = append(publications, publication)
	}
	if len(publications) != 0 && os.Getenv("NODE_AUTH_TOKEN") == "" {
		failures = append(failures, errors.New("NODE_AUTH_TOKEN is required for Bun registry publication"))
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("Bun publication preflight: %w", err)
	}
	if len(publications) == 0 {
		return nil
	}

	// Reuse credentials within a registry, but do not expose a different
	// registry's auth line to a package's publish task.
	configs := make(map[string]string)
	defer func() {
		for _, path := range configs {
			if err := os.Remove(path); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove npm credentials: %w", err))
			}
		}
	}()
	token := os.Getenv("NODE_AUTH_TOKEN")
	for _, publication := range publications {
		key := bunRegistryAuthScope(publication.registry)
		if _, exists := configs[key]; exists {
			continue
		}
		configPath, err := writeBunCredentials(key, token)
		if err != nil {
			return err
		}
		configs[key] = configPath
	}
	publisher := core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}
	for _, publication := range publications {
		configPath := configs[bunRegistryAuthScope(publication.registry)]
		fmt.Fprintf(stdout, "%s: %s %s\n", task, publication.name, version)
		if err := publisher.Run(ctx, publication.directory, []string{
			"PREMISE_PUBLISH_VERSION=" + version,
			"NPM_CONFIG_USERCONFIG=" + configPath,
		}, "run", task); err != nil {
			return fmt.Errorf("publish Bun package %s: %w", publication.name, err)
		}
	}
	return nil
}

func bunRegistryAuthScope(registry *url.URL) string {
	path := registry.Path
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return "//" + strings.ToLower(registry.Host) + path
}

func writeBunCredentials(scope, token string) (string, error) {
	config, err := os.CreateTemp("", "premise-npmrc-*")
	if err != nil {
		return "", fmt.Errorf("create npm credentials: %w", err)
	}
	path := config.Name()
	if err := config.Chmod(0o600); err != nil {
		config.Close()
		os.Remove(path)
		return "", fmt.Errorf("secure npm credentials: %w", err)
	}
	_, writeErr := fmt.Fprintf(config, "%s:_authToken=%s\n", scope, token)
	closeErr := config.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("write npm credentials: %w", err)
	}
	return path, nil
}
