// Package mise is the only boundary between Premise and the Mise executable.
package mise

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner executes Mise commands with a controlled working directory and environment.
type Runner struct {
	Stdout            io.Writer
	Stderr            io.Writer
	Ceiling           string
	RemoveEnvironment []string
}

// Run executes one Mise command.
func (runner Runner) Run(ctx context.Context, directory string, additions []string, arguments ...string) error {
	command := exec.CommandContext(ctx, "mise", arguments...)
	command.Dir = directory
	command.Env = runner.Environment(additions)
	command.Stdout = runner.Stdout
	command.Stderr = runner.Stderr
	return command.Run()
}

// TaskExists reports whether a Mise task selector resolves in directory.
func (runner Runner) TaskExists(ctx context.Context, directory, task string) (bool, error) {
	command := exec.CommandContext(ctx, "mise", "task", "info", task, "--json")
	command.Dir = directory
	command.Env = runner.Environment(nil)
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

// Environment returns the process environment used for Mise-aware commands.
func (runner Runner) Environment(additions []string) []string {
	removed := append([]string(nil), runner.RemoveEnvironment...)
	if runner.Ceiling != "" {
		removed = append(removed, "MISE_CEILING_PATHS")
	}
	for _, addition := range additions {
		if name, _, ok := strings.Cut(addition, "="); ok {
			removed = append(removed, name)
		}
	}
	environment := WithoutEnvironment(os.Environ(), removed...)
	if runner.Ceiling != "" {
		environment = append(environment, "MISE_CEILING_PATHS="+runner.Ceiling)
	}
	return append(environment, additions...)
}

// WithoutEnvironment removes named variables while preserving every other entry.
func WithoutEnvironment(environment []string, names ...string) []string {
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
