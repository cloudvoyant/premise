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
	"sort"
	"strings"
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
