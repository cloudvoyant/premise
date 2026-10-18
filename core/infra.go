package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gobwas/glob"
)

// InfraOptions selects an environment and a saved backend; project filtering stays in its stack.
type InfraOptions struct {
	Project, Environment, Backend, Branch string
	SecretsProvider                       string
	All, Yes, Shared                      bool
}

// InfraResolution is a pure, validated destination for an infrastructure operation.
type InfraResolution struct {
	Program                                   Project
	Stack, BackendName, BackendURL, Directory string
	SecretsProvider                           string
	Config                                    map[string]string
	Targets                                   []string
}

// Public API -----------------------------------------------------------------

// ResolveInfrastructure validates selection before any external command runs.
func ResolveInfrastructure(root string, options InfraOptions) (InfraResolution, error) {
	stack, err := InfraStack(options)
	if err != nil {
		return InfraResolution{}, err
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return InfraResolution{}, err
	}
	infra := manifest.Workspace.Infra
	if infra == nil {
		infra = &Infrastructure{}
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
	scope := "environment"
	if options.Shared {
		scope = "shared"
	}
	var program Project
	var targets []string
	for _, p := range manifest.Workspace.Projects {
		if p.Infra != nil && p.Infra.Role == "program" && p.Infra.Scope == scope {
			program = p
		}
		if p.Name == options.Project && p.Infra != nil {
			targets = p.Infra.Targets
		}
	}
	if program.Name == "" {
		return InfraResolution{}, fmt.Errorf("no %s infrastructure program", scope)
	}
	if options.Project != "" && options.Project != program.Name && len(targets) == 0 {
		return InfraResolution{}, fmt.Errorf("project %q has no declared resource targets", options.Project)
	}
	for app, binding := range infra.Bindings {
		if binding.Program != program.Name {
			continue
		}
		for _, p := range manifest.Workspace.Projects {
			if p.Name == app {
				if err := rejectSymlinkComponents(root, filepath.Join(root, p.Path, binding.Artifact)); err != nil {
					return InfraResolution{}, err
				}
			}
		}
	}
	directory, err := TemplateDirectory(root, program.Path)
	if err != nil {
		return InfraResolution{}, err
	}
	return InfraResolution{Program: program, Stack: stack, BackendName: name, BackendURL: backend.URL, Directory: directory, SecretsProvider: options.SecretsProvider, Config: environmentProgramConfig(manifest, settings.Config, options, stack, program), Targets: targets}, nil
}

// RunInfra executes a provider-neutral operation with explicit backend and stack.
func RunInfra(ctx context.Context, root, operation string, options InfraOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	resolution, err := ResolveInfrastructure(root, options)
	if err != nil {
		return err
	}
	if stderr != nil {
		fmt.Fprintf(stderr, "Infrastructure %s: %s stack=%s backend=%s (%s)\n", operation, resolution.Program.Name, resolution.Stack, resolution.BackendName, resolution.BackendURL)
	}
	runner := infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}
	if operation == "destroy" && strings.HasPrefix(resolution.Stack, "preview-") {
		exists, err := previewStackExists(ctx, runner, resolution)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
	}
	secrets := map[string]string{}
	if operation == "preview" || operation == "plan" || operation == "apply" {
		secrets, err = infrastructureSecrets(ctx, root, resolution, options)
	}
	if err != nil {
		return err
	}
	runner.secrets = secrets
	return executeInfra(ctx, runner, resolution, operation, options.Yes, secrets)
}

// TestInfrastructure runs declared program and component tests without a stack.
func TestInfrastructure(ctx context.Context, root, project string, stdin io.Reader, stdout, stderr io.Writer) error {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	count := 0
	for _, p := range manifest.Workspace.Projects {
		if p.Infra == nil || (project != "" && project != p.Name) {
			continue
		}
		count++
		if err := RunProjectTask(ctx, root, p.Name, "test", stdin, stdout, stderr); err != nil {
			return err
		}
	}
	if count == 0 {
		return fmt.Errorf("no infrastructure projects selected")
	}
	return nil
}

// BindInfra saves an idempotent manifest-owned artifact binding without changing stack scope.
func BindInfra(root, app, program, artifact string) error {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	if artifact == "" || filepath.IsAbs(artifact) || escapesRoot(artifact) {
		return fmt.Errorf("artifact must be a relative app path")
	}
	var owner, application Project
	for _, p := range manifest.Workspace.Projects {
		if p.Name == program {
			owner = p
		}
		if p.Name == app {
			application = p
		}
	}
	if application.Name == "" || application.Infra != nil {
		return fmt.Errorf("app %q is not declared", app)
	}
	if owner.Infra == nil || owner.Infra.Role != "program" {
		return fmt.Errorf("binding owner %q must be a program", program)
	}
	if manifest.Workspace.Infra == nil {
		manifest.Workspace.Infra = &Infrastructure{}
	}
	if manifest.Workspace.Infra.Bindings == nil {
		manifest.Workspace.Infra.Bindings = map[string]InfraBinding{}
	}
	binding := InfraBinding{Program: program, Artifact: artifact}
	if old, ok := manifest.Workspace.Infra.Bindings[app]; ok && old != binding {
		return fmt.Errorf("app %s already has a different binding", app)
	}
	manifest.Workspace.Infra.Bindings[app] = binding
	return SaveManifest(filepath.Join(root, ManifestFilename), manifest)
}

// InfraStack resolves a standard environment to an explicit Pulumi stack.
// Preview branches receive stable, collision-resistant names across processes.
func InfraStack(options InfraOptions) (string, error) {
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
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Stdin = r.stdin
	command.Stdout = r.stdout
	if name == "pulumi" && len(args) > 0 && args[0] == "login" {
		command.Stdout = r.stderr
	}
	command.Stderr = r.stderr
	command.Env = os.Environ()
	for key, value := range env {
		prefix := key + "="
		for i := len(command.Env) - 1; i >= 0; i-- {
			if strings.HasPrefix(command.Env[i], prefix) {
				command.Env = append(command.Env[:i], command.Env[i+1:]...)
			}
		}
		command.Env = append(command.Env, prefix+value)
	}
	var output, diagnostics *secretRedactor
	if !r.private {
		values := map[string]string{}
		for _, entry := range command.Env {
			key, value, ok := strings.Cut(entry, "=")
			if ok && value != "" && deploymentSecretKeys[key] {
				values[key] = value
			}

		}
		for key, value := range r.secrets {
			values[key] = value
		}
		if len(values) > 0 {
			out := command.Stdout
			if out == nil {
				out = io.Discard
			}
			diagnostic := command.Stderr
			if diagnostic == nil {
				diagnostic = io.Discard
			}
			output = &secretRedactor{destination: out, values: values}
			diagnostics = &secretRedactor{destination: diagnostic, values: values}
			command.Stdout = output
			command.Stderr = diagnostics
		}
	}
	runErr := command.Run()
	var flushErr error
	if output != nil {
		flushErr = errors.Join(output.Flush(), diagnostics.Flush())
	}
	if runErr != nil {
		return errors.Join(fmt.Errorf("%s operation failed: %w", name, runErr), flushErr)
	}
	return flushErr
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
	case "init", "preview", "apply", "output", "destroy", "refresh":
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
	if operation == "init" {
		args := []string{"stack", "select", d.Stack, "--create", "--non-interactive"}
		if d.SecretsProvider != "" {
			args = append(args, "--secrets-provider", d.SecretsProvider)
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

// previewStackExists makes merge and branch-deletion cleanup safe to repeat.
func previewStackExists(ctx context.Context, runner infraCommandRunner, d InfraResolution) (bool, error) {
	var output bytes.Buffer
	runner.stdout = &output
	if err := runner.run(ctx, d.Directory, map[string]string{"PULUMI_BACKEND_URL": d.BackendURL}, "pulumi", "stack", "ls", "--json"); err != nil {
		return false, fmt.Errorf("list preview stacks before cleanup: %w", err)
	}
	var stacks []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(output.Bytes(), &stacks); err != nil {
		return false, fmt.Errorf("decode preview stack list: %w", err)
	}
	for _, stack := range stacks {
		if stack.Name == d.Stack || strings.HasSuffix(stack.Name, "/"+d.Stack) {
			return true, nil
		}
	}
	return false, nil
}

func environmentProgramConfig(manifest Config, declared map[string]string, options InfraOptions, stack string, program Project) map[string]string {
	result := map[string]string{}
	for k, v := range declared {
		result[k] = v
	}
	result["premise:environment"] = options.Environment
	result["premise:branch"] = options.Branch
	result["premise:stack"] = stack
	if manifest.Workspace.Infra != nil {
		bindings := map[string]InfraBinding{}
		for app, binding := range manifest.Workspace.Infra.Bindings {
			if binding.Program == program.Name {
				bindings[app] = binding
			}
		}
		data, _ := json.Marshal(bindings)
		result["premise:bindings"] = string(data)
		artifacts := map[string]string{}
		for app, binding := range manifest.Workspace.Infra.Bindings {
			if binding.Program != program.Name {
				continue
			}
			for _, project := range manifest.Workspace.Projects {
				if project.Name == app {
					relative, _ := filepath.Rel(program.Path, filepath.Join(project.Path, binding.Artifact))
					artifacts[app] = filepath.ToSlash(relative)
				}
			}
		}
		artifactJSON, _ := json.Marshal(artifacts)
		result["artifacts"] = string(artifactJSON)
	}
	return result
}

// infrastructureSecrets uses the program's own fnox profile. The program decides
// which environment variables belong to each component; app bindings carry paths only.
func infrastructureSecrets(ctx context.Context, root string, d InfraResolution, options InfraOptions) (map[string]string, error) {
	if d.Program.Secrets == nil {
		return map[string]string{}, nil
	}
	return LoadProjectSecrets(ctx, root, d.Program.Name, options.Environment, options.Branch)
}

// The wire format is deliberately small: core depends on Pulumi's CLI plan,
// not its provider SDK or automation engine.
type infraPlanGoal struct {
	Type                 string              `json:"type"`
	Custom               bool                `json:"custom"`
	Parent               string              `json:"parent"`
	Dependencies         []string            `json:"dependencies"`
	PropertyDependencies map[string][]string `json:"propertyDependencies"`
	Provider             string              `json:"provider"`
}
type infraResourcePlan struct {
	Goal  *infraPlanGoal `json:"goal"`
	Steps []string       `json:"steps"`
}

func resolvePrerequisiteTargets(ctx context.Context, r infraRunner, d InfraResolution, patterns []string, env map[string]string) ([]string, error) {
	scopedEnv := map[string]string{}
	for k, v := range env {
		scopedEnv[k] = v
	}
	scopedEnv["PULUMI_BACKEND_URL"] = d.BackendURL
	env = scopedEnv
	directory, err := os.MkdirTemp("", "premise-preview-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "plan.json")
	if err := r.run(ctx, d.Directory, env, "pulumi", "preview", "--stack", d.Stack, "--non-interactive", "--save-plan", path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prerequisite preview: %w", err)
	}
	var versioned struct {
		Version int `json:"version"`
		Plan    struct {
			Resources map[string]infraResourcePlan `json:"resourcePlans"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(data, &versioned); err != nil {
		return nil, fmt.Errorf("decode prerequisite preview: %w", err)
	}
	if versioned.Version == 0 {
		var direct struct {
			Resources map[string]infraResourcePlan `json:"resourcePlans"`
		}
		if err := json.Unmarshal(data, &direct); err != nil {
			return nil, err
		}
		if direct.Resources != nil {
			return validatePrerequisiteCoverage(direct.Resources, patterns, d.Stack, d.Program.Name)
		}
	}
	if versioned.Version != 1 {
		return nil, fmt.Errorf("unsupported Pulumi plan version %d", versioned.Version)
	}
	return validatePrerequisiteCoverage(versioned.Plan.Resources, patterns, d.Stack, d.Program.Name)
}
func validatePrerequisiteCoverage(resources map[string]infraResourcePlan, patterns []string, stack, project string) ([]string, error) {
	selected := map[string]bool{}
	for _, pattern := range patterns {
		pattern = strings.ReplaceAll(strings.ReplaceAll(pattern, "{stack}", stack), "{project}", project)
		matcher, err := glob.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid prerequisite pattern %q", pattern)
		}
		matched := false
		for urn, resource := range resources {
			if matcher.Match(urn) {
				if resource.Goal == nil {
					return nil, fmt.Errorf("prerequisite %s is being deleted", urn)
				}
				selected[urn] = true
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("prerequisite pattern %q matches no resource", pattern)
		}
	}
	// Include component parents and providers needed to register the selected resources.
	changed := true
	for changed {
		changed = false
		for urn := range selected {
			goal := resources[urn].Goal
			if goal == nil {
				continue
			}
			for _, dependency := range []string{goal.Parent, providerURN(goal.Provider)} {
				if dependency == "" || selected[dependency] {
					continue
				}
				resource, ok := resources[dependency]
				if !ok || resource.Goal == nil {
					return nil, fmt.Errorf("prerequisite %s has unresolved parent/provider %s", urn, dependency)
				}
				if resource.Goal.Custom && !strings.HasPrefix(resource.Goal.Type, "pulumi:providers:") {
					return nil, fmt.Errorf("prerequisite %s requires explicit parent target %s", urn, dependency)
				}
				selected[dependency] = true
				changed = true
			}
		}
	}
	for urn := range selected {
		goal := resources[urn].Goal
		if goal == nil {
			continue
		}
		dependencies := append([]string{}, goal.Dependencies...)
		for _, values := range goal.PropertyDependencies {
			dependencies = append(dependencies, values...)
		}
		for _, dependency := range dependencies {
			if !selected[dependency] {
				return nil, fmt.Errorf("prerequisite %s depends on untargeted %s; declare it explicitly", urn, dependency)
			}
		}
	}
	result := make([]string, 0, len(selected))
	for urn := range selected {
		result = append(result, urn)
	}
	sort.Strings(result)
	return result, nil
}
func providerURN(reference string) string {
	if reference == "" {
		return ""
	}
	index := strings.LastIndex(reference, "::")
	if index < 0 {
		return reference
	}
	return reference[:index]
}
