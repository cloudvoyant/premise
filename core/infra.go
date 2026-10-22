package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cloudvoyant/premise/core/extensions"
	"github.com/cloudvoyant/premise/core/providers"
	"go.yaml.in/yaml/v3"
)

// Public API -----------------------------------------------------------------

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

// ResolveInfraOptions fills environment defaults and the current preview branch.
func ResolveInfraOptions(ctx context.Context, root string, options InfraOptions) (InfraOptions, error) {
	if options.Backend == "" {
		options.Backend = os.Getenv("PM_INFRA_BACKEND")
	}
	if options.Environment == "preview" && options.Branch == "" {
		branch, err := extensions.CurrentFeatureBranch(ctx, root)
		if err != nil {
			return options, err
		}
		options.Branch = branch
	}
	return options, nil
}

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
func RunInfra(ctx context.Context, root, operation string, options InfraOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := EnsureInfrastructureEnvironment(root, runningInCI()); err != nil {
		return err
	}
	resolution, err := ResolveInfrastructure(root, options)
	if err != nil {
		return err
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

// InfraQuery selects one exported value or one saved resource property.
type InfraQuery struct {
	Output      string
	Resource    string
	Type        string
	Property    string
	JSON        bool
	ShowSecrets bool
}

// QueryInfrastructure reads saved state without refreshing or applying resources.
// A secret value requires an explicit ShowSecrets request.
func QueryInfrastructure(ctx context.Context, root string, options InfraOptions, query InfraQuery, stdout, stderr io.Writer) error {
	if (query.Output == "") == (query.Resource == "") {
		return fmt.Errorf("select exactly one --output or --resource")
	}
	if query.Resource != "" && query.Property == "" {
		return fmt.Errorf("--resource requires --property, such as id or arn")
	}
	if query.Output != "" && (query.Property != "" || query.Type != "") {
		return fmt.Errorf("--property and --type apply only to resource queries")
	}
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	runner := infraCommandRunner{stdout: &output, stderr: stderr, private: true}
	environment := map[string]string{"PULUMI_BACKEND_URL": destination.BackendURL}
	if err := runner.run(ctx, destination.Directory, environment, "pulumi", "login", destination.BackendURL, "--non-interactive"); err != nil {
		return err
	}
	args := []string{"stack", "output", "--json"}
	if query.Resource != "" {
		args = []string{"stack", "export"}
	}
	args = append(args, "--stack", destination.Stack, "--non-interactive")
	if query.ShowSecrets {
		args = append(args, "--show-secrets")
	}
	if err := runner.run(ctx, destination.Directory, environment, "pulumi", args...); err != nil {
		return fmt.Errorf("query stack %s: %w", destination.Stack, err)
	}
	var value any
	if query.Output != "" {
		var outputs map[string]any
		if err := json.Unmarshal(output.Bytes(), &outputs); err != nil {
			return fmt.Errorf("read Pulumi stack outputs: %w", err)
		}
		var exists bool
		value, exists = outputs[query.Output]
		if !exists {
			return fmt.Errorf("stack %s has no output %q", destination.Stack, query.Output)
		}
	} else {
		var state pulumiCheckpoint
		if err := json.Unmarshal(output.Bytes(), &state); err != nil {
			return fmt.Errorf("read Pulumi stack resources: %w", err)
		}
		var matches []pulumiSavedResource
		for _, resource := range state.Deployment.Resources {
			// Interrupted replacements can leave an old resource with the same URN.
			// Queries select the current resource, not the pending deletion.
			if resource.Delete {
				continue
			}
			if resource.URN != query.Resource && !strings.HasSuffix(resource.URN, "::"+query.Resource) {
				continue
			}
			if query.Type != "" && query.Type != resource.Type {
				continue
			}
			matches = append(matches, resource)
		}
		if len(matches) == 0 {
			return fmt.Errorf("stack %s has no resource matching %q", destination.Stack, query.Resource)
		}
		if len(matches) != 1 {
			return fmt.Errorf("resource %q matches %d resources; select its full URN or add --type", query.Resource, len(matches))
		}
		resource := matches[0]
		switch query.Property {
		case "id":
			value = resource.ID
		case "urn":
			value = resource.URN
		case "type":
			value = resource.Type
		default:
			value = resource.Outputs
			for _, key := range strings.Split(query.Property, ".") {
				if wrapped, ok := value.(map[string]any); ok && wrapped["4dabf18193072939515e22adb298388d"] == "1b47061264138c4ac30d75fd1eb44270" {
					value, err = readablePulumiValue(value, query.ShowSecrets)
					if err != nil {
						return err
					}
				}
				fields, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("resource %q has no property %q", query.Resource, query.Property)
				}
				value, ok = fields[key]
				if !ok {
					return fmt.Errorf("resource %q has no property %q", query.Resource, query.Property)
				}
			}
		}
	}
	value, err = readablePulumiValue(value, query.ShowSecrets)
	if err != nil {
		return err
	}
	if !query.JSON {
		if text, ok := value.(string); ok {
			_, err := fmt.Fprintln(stdout, text)
			return err
		}
	}
	return json.NewEncoder(stdout).Encode(value)
}

// InfrastructureExists reports whether the selected stack has saved application resources.
// Missing and initialized-but-empty stacks both require a first infrastructure apply.
func InfrastructureExists(ctx context.Context, root string, options InfraOptions, stderr io.Writer) (bool, error) {
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		return false, err
	}
	var output bytes.Buffer
	runner := infraCommandRunner{stdout: &output, stderr: stderr, private: true}
	environment := map[string]string{"PULUMI_BACKEND_URL": destination.BackendURL}
	if err := runner.run(ctx, destination.Directory, environment, "pulumi", "login", destination.BackendURL, "--non-interactive"); err != nil {
		return false, err
	}
	exists, err := stackExists(ctx, runner, destination)
	if err != nil || !exists {
		return false, err
	}
	output.Reset()
	if err := runner.run(ctx, destination.Directory, environment, "pulumi", "stack", "export", "--stack", destination.Stack, "--non-interactive"); err != nil {
		return false, err
	}
	var state struct {
		Deployment struct {
			Resources []struct {
				Type   string `json:"type"`
				Delete bool   `json:"delete"`
			} `json:"resources"`
		} `json:"deployment"`
	}
	if err := json.Unmarshal(output.Bytes(), &state); err != nil {
		return false, err
	}
	for _, resource := range state.Deployment.Resources {
		if !resource.Delete && resource.Type != "" && !strings.HasPrefix(resource.Type, "pulumi:") {
			return true, nil
		}
	}
	return false, nil
}

// PrepareInfrastructure installs program dependencies and selects the configured stack.
func PrepareInfrastructure(ctx context.Context, root string, options InfraOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		return err
	}
	return prepareInfrastructure(ctx, destination, infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}, nil)
}

// EnsureInfrastructureEnvironment checks declared providers before infrastructure work.
func EnsureInfrastructureEnvironment(root string, ci bool) error {
	names, err := infrastructureProviderNames(root)
	if err != nil {
		return err
	}
	for _, name := range names {
		if provider, ok := providers.InfrastructureProvider(name); ok {
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
		provider, ok := providers.InfrastructureProvider(name)
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

// InfrastructureSetup selects an empty language starter or adopts existing code.
// PulumiVersion is persisted only in the workspace mise.toml.
type InfrastructureSetup struct {
	Language      string
	PulumiVersion string
}

// InitializeWorkspaceWithInfrastructure stages workspace files and Pulumi code
// before writing to the destination. Existing infrastructure is preserved.
func InitializeWorkspaceWithInfrastructure(ctx context.Context, root string, scaffold WorkspaceScaffold, setup InfrastructureSetup, stdin io.Reader, stdout, stderr io.Writer) (string, error) {
	if _, err := os.Lstat(filepath.Join(root, ManifestFilename)); err == nil {
		return "", fmt.Errorf("%s already exists", ManifestFilename)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := preflightWorkspace(root, scaffold.WorkflowAssets); err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(root, filepath.Join(root, "infra")); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp("", "premise-init-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	originalMise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if originalMise != nil {
		if err := os.WriteFile(filepath.Join(stage, "mise.toml"), originalMise, 0o644); err != nil {
			return "", err
		}
	}
	if _, err := InitializeWorkspace(stage, scaffold.MiseTemplate, scaffold.WorkflowAssets...); err != nil {
		return "", err
	}

	if err := ensureWorkflowAssets(stage, infrastructureWorkflows(scaffold.WorkflowAssets)); err != nil {
		return "", err
	}
	if err := stageInfrastructureProgram(ctx, root, stage, "infra", setup, stdin, stdout, stderr); err != nil {
		return "", err
	}
	manifest := NewManifest(filepath.Base(root))
	manifest.Workspace.Kind = ProjectKindMonorepo
	manifest.Workspace.Infra = &Infrastructure{
		Directory: "infra",
		Environments: map[string]InfraEnvironment{
			"dev": {}, "stage": {}, "prod": {}, "preview": {},
		},
	}
	if err := SaveManifest(filepath.Join(stage, ManifestFilename), manifest); err != nil {
		return "", err
	}
	if err := commitInfrastructureWorkspace(root, stage, map[string][]byte{"mise.toml": originalMise}); err != nil {
		return "", err
	}
	return filepath.Join(root, ManifestFilename), nil
}

// SetupInfrastructure adds a client-owned program and missing deployment workflows.
// Existing configuration, programs, tool pins, and workflows remain client-owned.
func SetupInfrastructure(ctx context.Context, root string, scaffold WorkspaceScaffold, setup InfrastructureSetup, stdin io.Reader, stdout, stderr io.Writer) error {
	manifestPath := filepath.Join(root, ManifestFilename)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	if manifest.Workspace.Kind != ProjectKindMonorepo {
		return fmt.Errorf("infrastructure setup requires a monorepo workspace")
	}
	directory := "infra"
	if manifest.Workspace.Infra != nil && manifest.Workspace.Infra.Directory != "" {
		directory = manifest.Workspace.Infra.Directory
	}
	if err := rejectSymlinkComponents(root, filepath.Join(root, directory)); err != nil {
		return err
	}
	var missing []WorkflowAsset
	for _, asset := range infrastructureWorkflows(scaffold.WorkflowAssets) {
		path := filepath.Join(root, asset.Path)
		if err := rejectSymlinkComponents(root, path); err != nil {
			return err
		}
		if info, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, asset)
		} else if err != nil {
			return err
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("workflow %s must be a regular file", asset.Path)
		}
	}
	if err := preflightWorkspace(root, missing); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "premise-infra-setup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	originals := map[string][]byte{}
	for _, relative := range []string{ManifestFilename, "mise.toml"} {
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			return err
		}
		originals[relative] = data
		if err := os.WriteFile(filepath.Join(stage, relative), data, 0o644); err != nil {
			return err
		}
	}
	if err := stageInfrastructureProgram(ctx, root, stage, directory, setup, stdin, stdout, stderr); err != nil {
		return err
	}
	if manifest.Workspace.Infra == nil {
		manifest.Workspace.Infra = &Infrastructure{
			Directory:    directory,
			Environments: map[string]InfraEnvironment{"dev": {}, "stage": {}, "prod": {}, "preview": {}},
		}
		if err := SaveManifest(filepath.Join(stage, ManifestFilename), manifest); err != nil {
			return err
		}
	}
	if err := ensureWorkflowAssets(stage, missing); err != nil {
		return err
	}
	return commitInfrastructureWorkspace(root, stage, originals)
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

type pulumiCheckpoint struct {
	Deployment struct {
		Resources []pulumiSavedResource `json:"resources"`
	} `json:"deployment"`
}

type pulumiSavedResource struct {
	Delete  bool           `json:"delete"`
	URN     string         `json:"urn"`
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Outputs map[string]any `json:"outputs"`
}

func readablePulumiValue(value any, showSecrets bool) (any, error) {
	switch typed := value.(type) {
	case string:
		if typed == "[secret]" && !showSecrets {
			return nil, fmt.Errorf("value is secret; request --show-secrets explicitly")
		}
	case map[string]any:
		if typed["4dabf18193072939515e22adb298388d"] == "1b47061264138c4ac30d75fd1eb44270" {
			if !showSecrets {
				return nil, fmt.Errorf("value is secret; request --show-secrets explicitly")
			}
			plaintext, exists := typed["value"]
			if !exists {
				return nil, fmt.Errorf("Pulumi did not return a decrypted secret value")
			}
			return readablePulumiValue(plaintext, true)
		}
		for key, child := range typed {
			readable, err := readablePulumiValue(child, showSecrets)
			if err != nil {
				return nil, err
			}
			typed[key] = readable
		}
	case []any:
		for i, child := range typed {
			readable, err := readablePulumiValue(child, showSecrets)
			if err != nil {
				return nil, err
			}
			typed[i] = readable
		}
	}
	return value, nil
}

func prepareInfrastructure(ctx context.Context, destination InfraResolution, runner infraCommandRunner, environment map[string]string) error {
	data, err := os.ReadFile(filepath.Join(destination.Directory, "mise.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	task := ""
	if err == nil {
		configuration, err := ExtractMiseConfig("infrastructure", data)
		if err != nil {
			return err
		}
		for _, name := range []string{"setup", "install"} {
			if _, exists := configuration.Tasks[name]; exists {
				task = name
				break
			}
		}
	}
	if task != "" {
		if err := runner.run(ctx, destination.Directory, environment, "mise", "run", task); err != nil {
			return err
		}
	} else if err := runner.run(ctx, destination.Directory, environment, "pulumi", "install", "--non-interactive"); err != nil {
		return err
	}
	return executeInfra(ctx, runner, destination, "init", true, environment)
}

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

type infraRunner interface {
	run(context.Context, string, map[string]string, string, ...string) error
}

type infraCommandRunner struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	private        bool
	secrets        map[string]string
}

func (r infraCommandRunner) run(ctx context.Context, dir string, env map[string]string, name string, args ...string) error {
	environment := os.Environ()
	for key, value := range env {
		environment = withoutEnvironment(environment, key)
		environment = append(environment, key+"="+value)
	}
	out, diagnostic := r.stdout, r.stderr
	if name == "pulumi" && len(args) > 0 && args[0] == "login" {
		out = r.stderr
	}
	var output, diagnostics *secretRedactor
	if !r.private {
		values := map[string]string{}
		for _, entry := range environment {
			key, value, ok := strings.Cut(entry, "=")
			if ok && value != "" && deploymentSecretKeys[key] {
				values[key] = value
			}
		}
		for key, value := range r.secrets {
			values[key] = value
		}
		if len(values) > 0 {
			if out == nil {
				out = io.Discard
			}
			if diagnostic == nil {
				diagnostic = io.Discard
			}
			output = &secretRedactor{destination: out, values: values}
			diagnostics = &secretRedactor{destination: diagnostic, values: values}
			out, diagnostic = output, diagnostics
		}
	}
	runner := extensions.Pulumi{Environment: environment, Stdin: r.stdin, Stdout: out, Stderr: diagnostic}
	runErr := runner.Run(ctx, dir, name, args...)
	var flushErr error
	if output != nil {
		flushErr = errors.Join(output.Flush(), diagnostics.Flush())
	}
	return errors.Join(runErr, flushErr)
}

func executeInfra(ctx context.Context, r infraRunner, d InfraResolution, operation string, yes bool, env map[string]string) error {
	scopedEnv := map[string]string{}
	for key, value := range env {
		scopedEnv[key] = value
	}
	scopedEnv["PULUMI_BACKEND_URL"] = d.BackendURL
	env = scopedEnv
	if operation == "destroy" && strings.HasPrefix(d.Stack, "preview-") {
		yes = true
	}
	if operation == "plan" {
		operation = "preview"
	}
	switch operation {
	case "init", "select", "preview", "apply", "output", "destroy", "refresh":
	default:
		return fmt.Errorf("unsupported infra operation %q", operation)
	}
	if operation == "destroy" && !yes {
		env["PULUMI_SKIP_CONFIRMATIONS"] = "false"
	}
	if operation == "destroy" && !strings.HasPrefix(d.Stack, "preview-") && yes {
		return fmt.Errorf("persistent environment destroy requires interactive confirmation")
	}
	if err := r.run(ctx, d.Directory, env, "pulumi", "login", d.BackendURL, "--non-interactive"); err != nil {
		return err
	}
	if operation == "init" || operation == "select" {
		args := []string{"stack", "select", d.Stack, "--non-interactive"}
		if operation == "init" {
			args = append(args, "--create")
		}
		if err := r.run(ctx, d.Directory, env, "pulumi", args...); err != nil {
			return err
		}
		keys := make([]string, 0, len(d.Config))
		for key := range d.Config {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := d.Config[key]
			if err := r.run(ctx, d.Directory, env, "pulumi", "config", "set", key, value, "--stack", d.Stack, "--non-interactive"); err != nil {
				return err
			}
		}
		return nil
	}
	args := []string{}
	switch operation {
	case "preview":
		args = []string{"preview"}
	case "apply":
		args = []string{"up"}
	case "destroy":
		args = []string{"destroy"}
	case "refresh":
		args = []string{"refresh"}
	case "output":
		args = []string{"stack", "output", "--json"}
	}
	args = append(args, "--stack", d.Stack)
	if operation == "preview" || operation == "output" {
		args = append(args, "--non-interactive")
	}
	if yes && (operation == "apply" || operation == "destroy" || operation == "refresh") {
		args = append(args, "--yes", "--non-interactive")
		if operation == "destroy" && strings.HasPrefix(d.Stack, "preview-") {
			args = append(args, "--remove")
		}
	}
	if operation == "apply" || operation == "preview" {
		for _, target := range d.Targets {
			args = append(args, "--target", target)
		}
	}
	return r.run(ctx, d.Directory, env, "pulumi", args...)
}

// stackExists makes merge and branch-deletion cleanup safe to repeat.
func stackExists(ctx context.Context, runner infraCommandRunner, d InfraResolution) (bool, error) {
	var output bytes.Buffer
	runner.stdout = &output
	if err := runner.run(ctx, d.Directory, map[string]string{"PULUMI_BACKEND_URL": d.BackendURL}, "pulumi", "stack", "ls", "--json"); err != nil {
		return false, fmt.Errorf("list Pulumi stacks: %w", err)
	}
	var stacks []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(output.Bytes(), &stacks); err != nil {
		return false, fmt.Errorf("decode Pulumi stack list: %w", err)
	}
	for _, stack := range stacks {
		if stack.Name == d.Stack || strings.HasSuffix(stack.Name, "/"+d.Stack) {
			return true, nil
		}
	}
	return false, nil
}

const defaultPulumiVersion = "3.268.0"

func stageInfrastructureProgram(ctx context.Context, root, stage, directory string, setup InfrastructureSetup, stdin io.Reader, stdout, stderr io.Writer) error {
	language, exists, err := existingPulumiLanguage(filepath.Join(root, directory, "Pulumi.yaml"))
	if err != nil {
		return err
	}
	if exists {
		if setup.Language != "" && pulumiRuntimeName(setup.Language) != pulumiRuntimeName(language) {
			return fmt.Errorf("%s uses %s; language %s would change the existing program", filepath.Join(directory, "Pulumi.yaml"), language, setup.Language)
		}
	} else {
		if setup.Language == "" {
			return fmt.Errorf("choose a Pulumi language with --infra-language during init or --language during infra setup")
		}
		language, err = pulumiLanguageLabel(setup.Language)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(root, directory)); err == nil {
			return fmt.Errorf("%s/ already exists without Pulumi.yaml; initialize that project before adopting it", directory)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := configureInfrastructureTools(filepath.Join(stage, "mise.toml"), language, setup.PulumiVersion, directory); err != nil {
		return err
	}
	if !exists {
		home, err := os.MkdirTemp("", "premise-pulumi-bootstrap-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(home)
		runner := miseRunner{
			Stdin: stdin, Stdout: stdout, Stderr: stderr,
			RemoveEnvironment: []string{"PULUMI_ACCESS_TOKEN", "PULUMI_BACKEND_URL"},
		}
		starter := "https://github.com/pulumi/templates/tree/master/" + language
		if err := runner.run(ctx, stage, []string{"MISE_TRUSTED_CONFIG_PATHS=" + stage, "PULUMI_HOME=" + home}, "exec", "--", "pulumi", "new", starter,
			"--generate-only", "--non-interactive", "--yes", "--name", filepath.Base(root),
			"--dir", filepath.Join(stage, directory)); err != nil {
			return fmt.Errorf("initialize %s Pulumi project: %w", language, err)
		}
	}
	return nil
}

func infrastructureWorkflows(assets []WorkflowAsset) []WorkflowAsset {
	var workflows []WorkflowAsset
	for _, asset := range assets {
		if asset.InfrastructureOnly {
			workflows = append(workflows, asset)
		}
	}
	return workflows
}

func existingPulumiLanguage(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var project struct {
		Runtime any `yaml:"runtime"`
	}
	if err := yaml.Unmarshal(data, &project); err != nil {
		return "", false, fmt.Errorf("read existing Pulumi runtime: %w", err)
	}
	name, _ := project.Runtime.(string)
	if runtime, ok := project.Runtime.(map[string]any); ok {
		name, _ = runtime["name"].(string)
	}
	if name == "" {
		return "", false, fmt.Errorf("%s must declare a Pulumi runtime", path)
	}
	return name, true, nil
}

func pulumiRuntimeName(language string) string {
	switch language {
	case "typescript", "javascript":
		return "nodejs"
	case "csharp", "fsharp":
		return "dotnet"
	default:
		return language
	}
}

func configureInfrastructureTools(path, language, version, directory string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	configuration, err := ExtractMiseConfig(path, data)
	if err != nil {
		return err
	}
	tools, ok := configuration.Root["tools"].(map[string]any)
	if !ok {
		tools = map[string]any{}
		configuration.Root["tools"] = tools
	}
	if version != "" {
		tools["pulumi"] = version
	} else if _, exists := tools["pulumi"]; !exists {
		tools["pulumi"] = defaultPulumiVersion
	}
	tool, fallback := "", ""
	switch language {
	case "go":
		tool, fallback = "go", "1.25.8"
	case "typescript", "javascript", "nodejs":
		tool, fallback = "node", "24"
	case "python":
		tool, fallback = "python", "3.13"
	}
	if tool != "" {
		if _, exists := tools[tool]; !exists {
			tools[tool] = fallback
		}
	}
	tasks, ok := configuration.Root["tasks"].(map[string]any)
	if !ok {
		tasks = map[string]any{}
		configuration.Root["tasks"] = tasks
	}
	if _, exists := tasks["infra:install"]; !exists {
		tasks["infra:install"] = map[string]any{"dir": directory, "run": "mise exec -- pulumi install"}
	}
	encoded, err := encodeMiseConfig(configuration)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func commitInfrastructureWorkspace(root, stage string, originals map[string][]byte) error {
	var files []string
	if err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Pulumi starter contains a non-regular file: %s", path)
		}
		relative, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(root, relative)
		if err := rejectSymlinkComponents(root, destination); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil && originals[relative] == nil {
			return fmt.Errorf("workspace file %s already exists", relative)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		files = append(files, relative)
		return nil
	}); err != nil {
		return err
	}
	var created []string
	completed := false
	defer func() {
		if !completed {
			for _, path := range created {
				os.Remove(path)
			}
			for relative, original := range originals {
				if original != nil {
					os.WriteFile(filepath.Join(root, relative), original, 0o644)
				}
			}
		}
	}()
	for _, relative := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(stage, relative))
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		if originals[relative] == nil {
			created = append(created, path)
		}
	}
	for _, directory := range []string{"apps", "libs"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			return err
		}
	}
	completed = true
	return nil
}

// pulumiLanguageLabel normalizes a template selection without accepting template URLs.
func pulumiLanguageLabel(language string) (string, error) {
	language = strings.TrimSpace(language)
	switch language {
	case "go", "typescript", "javascript", "python", "yaml", "csharp", "fsharp", "java":
		return language, nil
	default:
		return "", fmt.Errorf("--infra-language must name an empty Pulumi language starter: go, typescript, javascript, python, yaml, csharp, fsharp, or java")
	}
}
