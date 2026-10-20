package providers

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Private helpers ------------------------------------------------------------

var gitlabSecretKeys = []string{"GITLAB_TOKEN"}

func syncGitLabSecret(ctx context.Context, root, repository, environment, key, value string) error {
	probe := exec.CommandContext(ctx, "glab", "variable", "get", key, "--repo", repository, "--scope", environment)
	probe.Dir = root
	var diagnostics bytes.Buffer
	probe.Stderr = &diagnostics
	operation := "update"
	if err := probe.Run(); err != nil {
		if !strings.Contains(diagnostics.String(), "404") {
			return fmt.Errorf("inspect GitLab secret %s failed", key)
		}
		operation = "set"
	}
	command := exec.CommandContext(ctx, "glab", "variable", operation, key, "--repo", repository, "--scope", environment, "--masked", "--raw")
	command.Dir = root
	command.Stdin = strings.NewReader(value)
	if err := command.Run(); err != nil {
		return fmt.Errorf("sync GitLab secret %s failed; previous keys can already be updated", key)
	}
	return nil
}
