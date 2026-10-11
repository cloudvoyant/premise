package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// InfraOptions selects an environment and a saved backend; project filtering stays in its stack.
type InfraOptions struct {
	Project, Environment, Backend, Branch string
	All, Yes, Shared                      bool
}

// InfraResolution is a pure, validated destination for an infrastructure operation.
type InfraResolution struct {
	Program                                   Project
	Stack, BackendName, BackendURL, Directory string
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
		return InfraResolution{}, fmt.Errorf("workspace.infra is not configured")
	}
	env := options.Environment
	if options.Shared {
		env = "shared"
	}
	settings, ok := infra.Environments[env]
	if !ok {
		return InfraResolution{}, fmt.Errorf("environment %q is not configured", env)
	}
	name := options.Backend
	if name == "" {
		name = settings.Backend
	}
	if name == "" {
		name = infra.Backend
	}
	backend, ok := infra.Backends[name]
	if !ok {
		return InfraResolution{}, fmt.Errorf("backend %q is not declared", name)
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
	return InfraResolution{Program: program, Stack: stack, BackendName: name, BackendURL: backend.URL, Directory: directory, Config: environmentProgramConfig(manifest, settings.Config, options, stack, program), Targets: targets}, nil
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
	secrets := "{}"
	if operation == "preview" || operation == "plan" || operation == "apply" {
		secrets, err = infrastructureAppSecrets(ctx, root, resolution, options)
	}
	if err != nil {
		return err
	}
	return executeInfra(ctx, runner, resolution, operation, options.Yes, map[string]string{"PM_APP_SECRETS": secrets})
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
		return fmt.Errorf("workspace.infra is not configured")
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

// Private helpers ------------------------------------------------------------

type infraRunner interface {
	run(context.Context, string, map[string]string, string, ...string) error
}
type infraCommandRunner struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	private        bool
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
			if key == "PM_APP_SECRETS" {
				var apps map[string]map[string]string
				if json.Unmarshal([]byte(value), &apps) == nil {
					for app, secrets := range apps {
						for name, value := range secrets {
							if value != "" {
								values[app+":"+name] = value
							}
						}
					}
				}
			}
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
		if err := r.run(ctx, d.Directory, env, "pulumi", "stack", "select", d.Stack, "--create", "--non-interactive"); err != nil {
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

func infrastructureAppSecrets(ctx context.Context, root string, d InfraResolution, options InfraOptions) (string, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return "", err
	}
	values := map[string]map[string]string{}
	for app, binding := range manifest.Workspace.Infra.Bindings {
		if binding.Program != d.Program.Name {
			continue
		}
		for _, project := range manifest.Workspace.Projects {
			if project.Name == app && project.Secrets != nil {
				secrets, err := LoadProjectSecrets(ctx, root, app, options.Environment, options.Branch)
				if err != nil {
					return "", err
				}
				values[app] = secrets
			}
		}
	}
	data, err := json.Marshal(values)
	return string(data), err
}
