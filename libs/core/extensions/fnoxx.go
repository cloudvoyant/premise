package extensions

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Fnox runs commands with fnox-managed JSON secret files.
type Fnox struct {
	Environment    []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Public API -----------------------------------------------------------------

// Exec leaves secret resolution and temporary-file lifetime to fnox.
// Mise selects the fnox version declared by the client workspace.
func (runner Fnox) Exec(ctx context.Context, directory, config, profile string, arguments ...string) error {
	args := []string{"exec", "--", "fnox", "--config", config, "--profile", profile, "--no-defaults", "--no-daemon", "--non-interactive", "--if-missing", "error", "exec", "--"}
	command := exec.CommandContext(ctx, "mise", append(args, arguments...)...)
	command.Dir = directory
	command.Env = []string{}
	for _, entry := range runner.Environment {
		if !strings.HasPrefix(entry, "FNOX_") && !strings.HasPrefix(entry, "RUST_LOG=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Stdin, command.Stdout, command.Stderr = runner.Stdin, runner.Stdout, runner.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("fnox command failed: %w", err)
	}
	return nil
}
