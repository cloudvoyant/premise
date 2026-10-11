package core

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

// Public API -----------------------------------------------------------------

// InfraStack resolves a standard environment to an explicit Pulumi stack.
// Preview branches receive stable, collision-resistant names across processes.
func InfraStack(options InfraOptions) (string, error) {
	if options.Shared {
		if options.Branch != "" || (options.Environment != "" && options.Environment != "shared") {
			return "", fmt.Errorf("--shared cannot be combined with a branch or non-shared environment")
		}
		return "shared", nil
	}
	switch options.Environment {
	case "dev", "stage", "prod":
		if options.Branch != "" {
			return "", fmt.Errorf("--branch is only valid for preview environments")
		}
		return options.Environment, nil
	case "preview":
		if strings.TrimSpace(options.Branch) == "" {
			return "", fmt.Errorf("preview requires --branch")
		}
		slug := strings.Trim(previewBranchCharacters.ReplaceAllString(strings.ToLower(options.Branch), "-"), "-")
		if slug == "" {
			slug = "branch"
		}
		if len(slug) > 40 {
			slug = slug[:40]
		}
		digest := sha256.Sum256([]byte(options.Branch))
		return fmt.Sprintf("preview-%s-%x", slug, digest[:5]), nil
	default:
		return "", fmt.Errorf("--env must be dev, stage, prod, or preview; select shared with --shared")
	}
}

// Private helpers ------------------------------------------------------------

var previewBranchCharacters = regexp.MustCompile(`[^a-z0-9-]+`)
