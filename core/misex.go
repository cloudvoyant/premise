package core

// Mise extension responsibilities:
//   - provide the only process boundary to the Mise executable;
//   - apply workspace ceiling and environment isolation consistently;
//   - inspect and execute tasks for install, template, CI, and release modules.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

var publicationCredentialEnvironment = []string{
	"GITHUB_TOKEN",
	"GH_TOKEN",
	"CARGO_REGISTRY_TOKEN",
	"CARGO_TOKEN",
	"CRATES_TOKEN",
}

// miseRunner executes Mise commands with a controlled working directory and environment.
type miseRunner struct {
	Stdout            io.Writer
	Stderr            io.Writer
	Ceiling           string
	RemoveEnvironment []string
}

// run executes one Mise command.
func (runner miseRunner) run(ctx context.Context, directory string, additions []string, arguments ...string) error {
	command := exec.CommandContext(ctx, "mise", arguments...)
	command.Dir = directory
	command.Env = runner.environment(additions)
	command.Stdout = runner.Stdout
	command.Stderr = runner.Stderr
	return command.Run()
}

// taskExists reports whether a Mise task selector resolves in directory.
func (runner miseRunner) taskExists(ctx context.Context, directory, task string) (bool, error) {
	command := exec.CommandContext(ctx, "mise", "task", "info", task, "--json")
	command.Dir = directory
	command.Env = runner.environment(nil)
	var commandError bytes.Buffer
	command.Stdout = io.Discard
	command.Stderr = &commandError
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(commandError.String())
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 && strings.Contains(message, "Task not found") {
			return false, nil
		}
		if message != "" {
			return false, errors.New(message)
		}
		return false, err
	}
	return true, nil
}

// toolEnvironment resolves an environment containing explicitly selected Mise tools.
// Mise itself runs with the runner's sanitized environment; the returned environment
// can then be used to launch the intended tool without exposing credentials to Mise.
func (runner miseRunner) toolEnvironment(ctx context.Context, directory string, tools ...string) ([]string, error) {
	arguments := append([]string{"exec"}, tools...)
	arguments = append(arguments, "--", "env", "-0")
	command := exec.CommandContext(ctx, "mise", arguments...)
	command.Dir = directory
	command.Env = runner.environment(nil)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = runner.Stderr
	if err := command.Run(); err != nil {
		return nil, err
	}
	entries := strings.Split(strings.TrimSuffix(output.String(), "\x00"), "\x00")
	if len(entries) == 1 && entries[0] == "" {
		return nil, errors.New("mise returned an empty tool environment")
	}
	return entries, nil
}

// environment returns the process environment used for Mise-aware commands.
func (runner miseRunner) environment(additions []string) []string {
	removed := append([]string(nil), publicationCredentialEnvironment...)
	removed = append(removed, runner.RemoveEnvironment...)
	if runner.Ceiling != "" {
		removed = append(removed, "MISE_CEILING_PATHS")
	}
	for _, addition := range additions {
		if name, _, ok := strings.Cut(addition, "="); ok {
			removed = append(removed, name)
		}
	}
	environment := withoutEnvironment(os.Environ(), removed...)
	if runner.Ceiling != "" {
		environment = append(environment, "MISE_CEILING_PATHS="+runner.Ceiling)
	}
	return append(environment, additions...)
}

// withoutEnvironment removes named variables while preserving every other entry.
func withoutEnvironment(environment []string, names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if _, remove := blocked[name]; ok && remove {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
