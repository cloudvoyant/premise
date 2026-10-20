package core

import (
	"bytes"
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

	"github.com/gobwas/glob"
)

// Private helpers ------------------------------------------------------------

type infraRunner interface {
	run(context.Context, string, map[string]string, string, ...string) error
}

type infraCommandRunner struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	private        bool
	secrets        map[string]string
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

func (r infraCommandRunner) run(ctx context.Context, dir string, env map[string]string, name string, args ...string) error {
	executable, arguments := name, args
	if name == "pulumi" {
		// Resolve the workspace tool pin for every operation, including state queries.
		executable = "mise"
		arguments = append([]string{"exec", "--", "pulumi"}, args...)
	}
	command := exec.CommandContext(ctx, executable, arguments...)
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
	if name == "pulumi" {
		command.Env = withEmptyPulumiPassphrase(command.Env)
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

// State currently uses an empty passphrase. Custom encryption is deferred.
func withEmptyPulumiPassphrase(environment []string) []string {
	environment = withoutEnvironment(environment, "PULUMI_CONFIG_PASSPHRASE", "PULUMI_CONFIG_PASSPHRASE_FILE")
	return append(environment, "PULUMI_CONFIG_PASSPHRASE=")
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
			return validatePrerequisiteCoverage(direct.Resources, patterns, d.Stack, d.ProjectName)
		}
	}
	if versioned.Version != 1 {
		return nil, fmt.Errorf("unsupported Pulumi plan version %d", versioned.Version)
	}
	return validatePrerequisiteCoverage(versioned.Plan.Resources, patterns, d.Stack, d.ProjectName)
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
