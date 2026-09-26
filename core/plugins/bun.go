package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/cloudvoyant/premise/core"
)

type Bun struct{}

func (Bun) ID() string                       { return "bun" }
func (Bun) Detect(root string) (bool, error) { return isBunRegistry(root) }
func (Bun) IsPublic(root string, template core.Template) (bool, error) {
	return bunTemplateIsPublic(root, template)
}
func (Bun) ShouldPublishPackage(root string, template core.Template) (bool, error) {
	return bunTemplateShouldPublish(root, template)
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

type bunPackage struct {
	Name          string `json:"name"`
	Private       bool   `json:"private"`
	PublishConfig struct {
		Access   string `json:"access"`
		Registry string `json:"registry"`
	} `json:"publishConfig"`
}

func isBunRegistry(root string) (bool, error) {
	packageFile, err := core.IsRegularFile(filepath.Join(root, "package.json"))
	if err != nil || !packageFile {
		return false, err
	}
	return core.IsRegularFile(filepath.Join(root, "bunfig.toml"))
}

func inspectBunTemplatePackage(root string, template core.Template) (bunPackage, bool, error) {
	directory, err := core.TemplateDirectory(root, template.Path)
	if err != nil {
		return bunPackage{}, false, err
	}
	path := filepath.Join(directory, "package.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return bunPackage{}, false, nil
	}
	if err != nil {
		return bunPackage{}, false, fmt.Errorf("read Bun package %s: %w", path, err)
	}
	var pkg bunPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		return bunPackage{}, false, fmt.Errorf("parse Bun package %s: %w", path, err)
	}
	if pkg.Name == "" {
		return bunPackage{}, false, fmt.Errorf("Bun package %s has no name", path)
	}
	return pkg, true, nil
}

func bunTemplateIsPublic(root string, template core.Template) (bool, error) {
	pkg, found, err := inspectBunTemplatePackage(root, template)
	return found && !pkg.Private && pkg.PublishConfig.Access == "public", err
}

func bunPackageEligible(pkg bunPackage) bool {
	return !pkg.Private && pkg.PublishConfig.Registry != ""
}

func bunTemplateShouldPublish(root string, template core.Template) (bool, error) {
	pkg, found, err := inspectBunTemplatePackage(root, template)
	return found && bunPackageEligible(pkg), err
}

type bunPublication struct {
	directory string
	name      string
	registry  *url.URL
}

func publishBunPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	version = strings.TrimPrefix(version, "v")
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		return fmt.Errorf("invalid Bun release version %q: %w", version, err)
	}
	if task != "publish" && task != "publish:rc" {
		return fmt.Errorf("unsupported Bun publication task %q", task)
	}
	if (task == "publish:rc") != (parsed.Prerelease() != "") || parsed.Metadata() != "" {
		return fmt.Errorf("Bun %s requires a matching SemVer version, got %s", task, version)
	}
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		return fmt.Errorf("load Bun registry manifest: %w", err)
	}
	publications := []bunPublication{}
	preflight := core.MiseTaskRunner{Stderr: stderr}
	for _, template := range manifest.DeclaredTemplates() {
		pkg, found, err := inspectBunTemplatePackage(root, template)
		if err != nil {
			return err
		}
		if !found || !bunPackageEligible(pkg) {
			fmt.Fprintf(stdout, "skip: %s Bun registry publication disabled\n", template.Name)
			continue
		}
		registry, err := url.Parse(pkg.PublishConfig.Registry)
		if err != nil || registry.Host == "" || registry.Scheme != "https" || registry.RawQuery != "" || registry.User != nil {
			return fmt.Errorf("Bun package %s has invalid HTTPS registry %q", pkg.Name, pkg.PublishConfig.Registry)
		}
		directory, err := core.TemplateDirectory(root, template.Path)
		if err != nil {
			return err
		}
		exists, err := preflight.TaskExists(ctx, directory, task)
		if err != nil {
			return fmt.Errorf("inspect Bun package %s task %s: %w", pkg.Name, task, err)
		}
		if !exists {
			return fmt.Errorf("Bun package %s does not define task %s", pkg.Name, task)
		}
		publications = append(publications, bunPublication{directory: directory, name: pkg.Name, registry: registry})
	}
	if len(publications) == 0 {
		return nil
	}
	token := os.Getenv("NODE_AUTH_TOKEN")
	if token == "" {
		return errors.New("NODE_AUTH_TOKEN is required for Bun registry publication")
	}
	publisher := core.MiseTaskRunner{Stdout: stdout, Stderr: stderr}
	for _, publication := range publications {
		config, err := os.CreateTemp("", "premise-npmrc-*")
		if err != nil {
			return fmt.Errorf("create npm credentials: %w", err)
		}
		configPath := config.Name()
		if err := config.Chmod(0o600); err != nil {
			config.Close()
			os.Remove(configPath)
			return fmt.Errorf("secure npm credentials: %w", err)
		}
		_, writeErr := fmt.Fprintf(config, "//%s%s:_authToken=%s\n", publication.registry.Host, publication.registry.Path, token)
		closeErr := config.Close()
		if writeErr != nil || closeErr != nil {
			os.Remove(configPath)
			return errors.Join(writeErr, closeErr)
		}
		fmt.Fprintf(stdout, "%s: %s %s\n", task, publication.name, version)
		err = publisher.Run(ctx, publication.directory, []string{
			"PREMISE_PUBLISH_VERSION=" + version,
			"NPM_CONFIG_USERCONFIG=" + configPath,
		}, "run", task)
		removeErr := os.Remove(configPath)
		if err != nil {
			return errors.Join(fmt.Errorf("publish Bun package %s: %w", publication.name, err), removeErr)
		}
		if removeErr != nil {
			return fmt.Errorf("remove npm credentials: %w", removeErr)
		}
	}
	return nil
}
