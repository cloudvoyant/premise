package extensions

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Pulumi runs infrastructure tools with the client's Mise tool versions.
type Pulumi struct {
	Environment    []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Public API -----------------------------------------------------------------

// Run launches Pulumi through Mise, or the infrastructure program's Mise tasks.
func (runner Pulumi) Run(ctx context.Context, directory, name string, arguments ...string) error {
	executable := name
	if name == "pulumi" {
		executable = "mise"
		arguments = append([]string{"exec", "--", "pulumi"}, arguments...)
	}
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = directory
	command.Env = runner.Environment
	if name == "pulumi" {
		command.Env = withEmptyStatePassphrase(command.Env)
	}
	command.Stdin, command.Stdout, command.Stderr = runner.Stdin, runner.Stdout, runner.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s operation failed: %w", name, err)
	}
	return nil
}

// Private helpers ------------------------------------------------------------

// withEmptyStatePassphrase overrides inherited passphrase settings until backend encryption is available.
func withEmptyStatePassphrase(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key != "PULUMI_CONFIG_PASSPHRASE" && key != "PULUMI_CONFIG_PASSPHRASE_FILE" {
			result = append(result, entry)
		}
	}
	return append(result, "PULUMI_CONFIG_PASSPHRASE=")
}
