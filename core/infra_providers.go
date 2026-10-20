package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core/providers"
)

// InfraProvider authenticates deployment tools and checks their standard variables.
// It never resolves application secrets.
type InfraProvider interface {
	EnsureEnvironment(ci bool) error
	Authenticate(context.Context, string, bool, io.Reader, io.Writer, io.Writer) (map[string]string, error)
}

// Public API -----------------------------------------------------------------

// InfrastructureProvider returns a supported provider, independently of Pulumi resources.
func InfrastructureProvider(name string) (InfraProvider, bool) {
	switch name {
	case "gcp":
		return providers.GCP{}, true
	case "neon":
		return providers.Neon{}, true
	case "vercel":
		return providers.Vercel{}, true
	default:
		return nil, false
	}
}

// EnsureInfrastructureEnvironment checks declared providers before infrastructure work.
func EnsureInfrastructureEnvironment(root string, ci bool) error {
	names, err := infrastructureProviderNames(root)
	if err != nil {
		return err
	}
	for _, name := range names {
		if provider, ok := InfrastructureProvider(name); ok {
			if err := provider.EnsureEnvironment(ci); err != nil {
				return err
			}
		}
	}
	return nil
}

// AuthenticateInfrastructure prepares native credentials, then the optional root fallback.
// Returned variables contain credential file paths, never token values.
func AuthenticateInfrastructure(ctx context.Context, root string, ci bool, stdin io.Reader, stdout, stderr io.Writer) (map[string]string, error) {
	names, err := infrastructureProviderNames(root)
	if err != nil {
		return nil, err
	}
	environment := map[string]string{}
	completed := false
	var createdDirectories []string
	defer func() {
		if !completed {
			for _, directory := range createdDirectories {
				os.RemoveAll(directory)
			}
		}
	}()
	var unsupported []string
	for _, name := range names {
		provider, ok := InfrastructureProvider(name)
		if !ok {
			unsupported = append(unsupported, name)
			continue
		}
		values, err := provider.Authenticate(ctx, root, ci, stdin, stdout, stderr)
		if err != nil {
			return nil, err
		}
		if ci && name == "gcp" && os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
			if path := values["GOOGLE_APPLICATION_CREDENTIALS"]; filepath.IsAbs(path) {
				createdDirectories = append(createdDirectories, filepath.Dir(path))
			}
		}
		for key, value := range values {
			environment[key] = value
		}
	}
	if ci {
		data, err := os.ReadFile(filepath.Join(root, "mise.toml"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		var fallback bool
		if err == nil {
			config, err := ExtractMiseConfig("workspace", data)
			if err != nil {
				return nil, err
			}
			_, fallback = config.Tasks["ci:auth"]
		}
		if fallback {
			additions := []string{}
			for key, value := range environment {
				additions = append(additions, key+"="+value)
			}
			runner := miseRunner{Stdin: stdin, Stdout: stdout, Stderr: stderr, Ceiling: filepath.Dir(root)}
			if err := runner.run(ctx, root, additions, "run", "ci:auth"); err != nil {
				return nil, fmt.Errorf("root ci:auth failed: %w", err)
			}
		} else if len(unsupported) > 0 {
			return nil, fmt.Errorf("unsupported infrastructure providers %s: declare ci:auth in the root mise.toml", strings.Join(unsupported, ", "))
		}
	} else if len(unsupported) > 0 {
		return nil, fmt.Errorf("authenticate unsupported providers %s using their own CLIs", strings.Join(unsupported, ", "))
	}
	completed = true
	return environment, nil
}

// Private helpers ------------------------------------------------------------

func infrastructureProviderNames(root string) ([]string, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	if manifest.Workspace.Infra == nil {
		return nil, nil
	}
	return manifest.Workspace.Infra.Providers, nil
}

func runningInCI() bool {
	return os.Getenv("CI") == "true" || os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("GITLAB_CI") == "true"
}
