package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// InfraOptions selects an environment stack and its saved backend.
type InfraOptions struct {
	Environment string
	Backend     string
	Branch      string
	Yes         bool
	Shared      bool
}

// InfraResolution is a pure, validated destination for an infrastructure operation.
type InfraResolution struct {
	ProjectName string
	Stack       string
	BackendName string
	BackendURL  string
	Directory   string
	Config      map[string]string
	Targets     []string
}

// Public API -----------------------------------------------------------------

// ResolveInfrastructure validates selection before any external command runs.
func ResolveInfrastructure(root string, options InfraOptions) (InfraResolution, error) {
	stack, err := ResolveStackName(options)
	if err != nil {
		return InfraResolution{}, err
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return InfraResolution{}, err
	}
	infra := manifest.Workspace.Infra
	if infra == nil {
		return InfraResolution{}, fmt.Errorf("workspace.infra is not configured; initialize client infrastructure with pm init --infra")
	}
	env := options.Environment
	if options.Shared {
		env = "shared"
	}
	settings := infra.Environments[env]
	name := options.Backend
	if name == "" {
		name = settings.Backend
	}
	if name == "" {
		name = infra.Backend
	}
	backend, ok := infra.Backends[name]
	if name == "" {
		name = "environment"
		backend.URL = os.Getenv("PULUMI_BACKEND_URL")
		if backend.URL == "" {
			return InfraResolution{}, fmt.Errorf("set PULUMI_BACKEND_URL or declare a named infrastructure backend")
		}
	} else if !ok {
		return InfraResolution{}, fmt.Errorf("backend %q is not declared", name)
	}
	if err := validateBackend(backend); err != nil {
		return InfraResolution{}, err
	}
	relative := infra.Directory
	if relative == "" {
		relative = "infra"
	}
	if options.Shared && infra.SharedDirectory != "" {
		relative = infra.SharedDirectory
	}
	directory, err := TemplateDirectory(root, relative)
	if err != nil {
		return InfraResolution{}, fmt.Errorf("open Pulumi project directory: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "Pulumi.yaml"))
	if err != nil {
		return InfraResolution{}, fmt.Errorf("read %s/Pulumi.yaml: %w", relative, err)
	}
	var project struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &project); err != nil {
		return InfraResolution{}, fmt.Errorf("read Pulumi project metadata: %w", err)
	}
	if err := ValidateProjectName(project.Name); err != nil {
		return InfraResolution{}, fmt.Errorf("Pulumi.yaml name: %w", err)
	}
	return InfraResolution{
		ProjectName: project.Name,
		Stack:       stack,
		BackendName: name,
		BackendURL:  backend.URL,
		Directory:   directory,
		Config:      environmentProgramConfig(settings.Config, options, stack),
	}, nil
}

// RunInfra executes a provider-neutral operation with explicit backend and stack.
func RunInfra(ctx context.Context, root, operation string, options InfraOptions, stdin io.Reader, stdout, stderr io.Writer) (resultErr error) {
	if err := EnsureInfrastructureEnvironment(root, runningInCI()); err != nil {
		return err
	}
	resolution, err := ResolveInfrastructure(root, options)
	if err != nil {
		return err
	}
	if operation == "init" || operation == "apply" || operation == "destroy" {
		release, err := acquireInfrastructureOwnership(ctx, root, resolution)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, release()) }()
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "Infrastructure %s: %s stack=%s backend=%s (%s)\n", operation, resolution.ProjectName, resolution.Stack, resolution.BackendName, resolution.BackendURL)
	}
	runner := infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}
	if operation == "destroy" && strings.HasPrefix(resolution.Stack, "preview-") {
		exists, err := stackExists(ctx, runner, resolution)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}
	return executeInfra(ctx, runner, resolution, operation, options.Yes, nil)
}

// ResolveStackName returns the Pulumi stack name for an environment and preview branch.
// Preview branches receive stable, collision-resistant names across processes.
func ResolveStackName(options InfraOptions) (string, error) {
	if options.Shared {
		if options.Branch != "" || (options.Environment != "" && options.Environment != "shared") {
			return "", fmt.Errorf("--shared cannot be combined with a branch or non-shared environment")
		}
		return "shared", nil
	}
	switch options.Environment {
	case "dev", "stage", "prod":
		if options.Branch != "" {
			return "", fmt.Errorf("--branch is only valid for preview environments")
		}
		return options.Environment, nil
	case "preview":
		if strings.TrimSpace(options.Branch) == "" {
			return "", fmt.Errorf("preview requires --branch")
		}
		slug := strings.Trim(previewBranchCharacters.ReplaceAllString(strings.ToLower(options.Branch), "-"), "-")
		if slug == "" {
			slug = "branch"
		}
		if len(slug) > 40 {
			slug = slug[:40]
		}
		digest := sha256.Sum256([]byte(options.Branch))
		return fmt.Sprintf("preview-%s-%x", slug, digest[:5]), nil
	default:
		return "", fmt.Errorf("--env must be dev, stage, prod, or preview; select shared with --shared")
	}
}

// Private helpers ------------------------------------------------------------

var previewBranchCharacters = regexp.MustCompile(`[^a-z0-9-]+`)

func environmentProgramConfig(declared map[string]string, options InfraOptions, stack string) map[string]string {
	result := map[string]string{}
	for key, value := range declared {
		result[key] = value
	}
	result["premise:environment"] = options.Environment
	result["premise:branch"] = options.Branch
	result["premise:stack"] = stack
	return result
}
