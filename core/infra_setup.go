package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Public API -----------------------------------------------------------------

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
func PrepareInfrastructure(ctx context.Context, root string, options InfraOptions, stdin io.Reader, stdout, stderr io.Writer) (resultErr error) {
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		return err
	}
	release, err := acquireInfrastructureOwnership(ctx, root, destination)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	return prepareInfrastructure(ctx, destination, infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}, nil)
}

// Private helpers ------------------------------------------------------------

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
