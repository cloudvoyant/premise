package core

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Public API -----------------------------------------------------------------

// CurrentFeatureBranch returns the checkout's branch for preview environments.
// Detached HEAD and the main or master branches require an explicit branch.
func CurrentFeatureBranch(ctx context.Context, root string) (string, error) {
	command := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("preview requires --branch outside a feature-branch checkout")
	}
	branch := strings.TrimSpace(string(output))
	if branch == "main" || branch == "master" {
		return "", fmt.Errorf("preview requires a feature branch")
	}
	return branch, nil
}
