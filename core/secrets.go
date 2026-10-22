package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core/extensions"
	"github.com/cloudvoyant/premise/core/providers"
)

// Public API -----------------------------------------------------------------

// PullProjectSecrets runs optional project secrets:pull tasks with the selected fnox profile.
// The task writes its env files; fnox.toml owns all provider and secret configuration.
func PullProjectSecrets(ctx context.Context, root, project, environment, branch string) error {
	if _, err := ResolveStackName(InfraOptions{Environment: environment, Branch: branch}); err != nil {
		return err
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range manifest.Workspace.Projects {
		if project != "" && candidate.Name != project {
			continue
		}
		directory, err := TemplateDirectory(root, candidate.Path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(directory, "mise.toml"))
		if err != nil {
			return err
		}
		config, err := ExtractMiseConfig(candidate.Name, data)
		if err != nil {
			return err
		}
		if _, exists := config.Tasks["secrets:pull"]; !exists {
			continue
		}
		found = true
		fnox := filepath.Join(directory, "fnox.toml")
		if err := rejectSymlinkComponents(directory, fnox); err != nil {
			return err
		}
		if _, err := os.Stat(fnox); err != nil {
			return fmt.Errorf("project %s secrets:pull requires fnox.toml: %w", candidate.Name, err)
		}
		values := withoutEnvironment(os.Environ(), "PM_ENV", "PM_BRANCH", "MISE_CEILING_PATHS")
		values = append(values, "PM_ENV="+environment, "PM_BRANCH="+branch, "MISE_CEILING_PATHS="+filepath.Dir(root))
		runner := extensions.Fnox{Environment: values, Stdout: io.Discard, Stderr: io.Discard}
		if err := runner.Exec(ctx, directory, fnox, environment, "mise", "run", "secrets:pull"); err != nil {
			return fmt.Errorf("project %s secrets:pull failed: %w", candidate.Name, err)
		}
	}
	if !found {
		return fmt.Errorf("no selected project declares a secrets:pull task")
	}
	return nil
}

// Private helpers ------------------------------------------------------------

var deploymentSecretKeys = providers.AdministrationSecretKeys()

type secretRedactor struct {
	destination io.Writer
	values      map[string]string
	buffer      bytes.Buffer
}

func (writer *secretRedactor) Write(data []byte) (int, error) {
	return writer.buffer.Write(data)
}

func (writer *secretRedactor) Flush() error {
	text := writer.buffer.String()
	for _, value := range writer.values {
		text = strings.ReplaceAll(text, value, "[REDACTED]")
	}
	_, err := io.WriteString(writer.destination, text)
	return err
}
