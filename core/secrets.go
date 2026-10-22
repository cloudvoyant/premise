package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core/extensions"
	"github.com/cloudvoyant/premise/core/providers"
)

// ProjectSecrets selects fnox profiles and an optional generated env file.
// Secret references and JSON file declarations belong to fnox.toml.
type ProjectSecrets struct {
	Config   string            `yaml:"config"`
	Profiles map[string]string `yaml:"profiles"`
	File     string            `yaml:"file,omitempty"`
}

// Public API -----------------------------------------------------------------

// PullProjectSecrets runs each selected project's secrets:pull task under fnox.
// The task emits an env file combining JSON secrets and environment infra queries.
func PullProjectSecrets(ctx context.Context, root, project, environment, branch string) error {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	type pendingFile struct {
		path string
		data []byte
	}
	var pending []pendingFile
	for _, candidate := range manifest.Workspace.Projects {
		if candidate.Secrets == nil || (project != "" && project != candidate.Name) {
			continue
		}
		directory, config, profile, err := resolveProjectSecrets(root, candidate, environment, branch)
		if err != nil {
			return err
		}
		name := candidate.Secrets.File
		if name == "" {
			name = ".env.local"
		}
		path := filepath.Join(directory, name)
		if err := rejectSymlinkComponents(root, path); err != nil {
			return err
		}
		check := exec.CommandContext(ctx, "git", "check-ignore", "--quiet", "--", path)
		check.Dir = root
		if err := check.Run(); err != nil {
			return fmt.Errorf("secret destination %s must be Git-ignored", path)
		}
		var output bytes.Buffer
		runner := extensions.Fnox{Environment: secretTaskEnvironment(root, environment, branch), Stdout: &output}
		if err := runner.Exec(ctx, directory, config, profile, "mise", "run", "--quiet", "secrets:pull", "--", environment); err != nil {
			return err
		}
		if output.Len() == 0 {
			return fmt.Errorf("%s secrets:pull produced an empty env file", candidate.Name)
		}
		pending = append(pending, pendingFile{path, output.Bytes()})
	}
	if len(pending) == 0 {
		return errors.New("no matching application secret configuration")
	}
	for _, file := range pending {
		if err := writePrivateSecretFile(file.path, file.data); err != nil {
			return err
		}
	}
	return nil
}

// ExecProjectSecrets delegates secret resolution and temporary-file cleanup to fnox.
// Local runs and CI use the same profile and command path.
func ExecProjectSecrets(ctx context.Context, root, project, environment, branch string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("a command after -- is required")
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	for _, candidate := range manifest.Workspace.Projects {
		if candidate.Name != project {
			continue
		}
		directory, config, profile, err := resolveProjectSecrets(root, candidate, environment, branch)
		if err != nil {
			return err
		}
		runner := extensions.Fnox{Environment: secretTaskEnvironment(root, environment, branch), Stdin: stdin, Stdout: stdout, Stderr: stderr}
		return runner.Exec(ctx, directory, config, profile, arguments...)
	}
	return fmt.Errorf("%w: %s", ErrProjectNotFound, project)
}

// Private helpers ------------------------------------------------------------

var deploymentSecretKeys = providers.AdministrationSecretKeys()

func resolveProjectSecrets(root string, project Project, environment, branch string) (directory, config, profile string, err error) {
	if project.Secrets == nil {
		return "", "", "", fmt.Errorf("project %s does not declare application secrets", project.Name)
	}
	if err = validateProjectSecrets(project); err != nil {
		return
	}
	stack, err := ResolveStackName(InfraOptions{Environment: environment, Branch: branch})
	if err != nil {
		return
	}
	profile = project.Secrets.Profiles[environment]
	if profile == "" {
		return "", "", "", fmt.Errorf("project %s has no secret profile for %s", project.Name, environment)
	}
	profile = strings.ReplaceAll(profile, "{branch}", strings.TrimPrefix(stack, "preview-"))
	directory, err = TemplateDirectory(root, project.Path)
	if err != nil {
		return
	}
	config = filepath.Join(directory, project.Secrets.Config)
	err = rejectSymlinkComponents(directory, config)
	return
}

func validateProjectSecrets(project Project) error {
	settings := project.Secrets
	if settings == nil {
		return nil
	}
	if settings.Config == "" || filepath.IsAbs(settings.Config) || settings.Config == ".." || strings.HasPrefix(filepath.Clean(settings.Config), ".."+string(filepath.Separator)) {
		return errors.New("secret config must be a path inside the application")
	}
	if len(settings.Profiles) == 0 {
		return errors.New("secret environment profiles are required")
	}
	if settings.File != "" && (filepath.Base(settings.File) != settings.File || !strings.HasPrefix(settings.File, ".env")) {
		return errors.New("secret file must be a local .env filename")
	}
	for environment, profile := range settings.Profiles {
		if environment != "dev" && environment != "stage" && environment != "prod" && environment != "preview" {
			return fmt.Errorf("unknown secret environment %q", environment)
		}
		if profile == "" || strings.ContainsAny(profile, ",\r\n") {
			return errors.New("one explicit fnox profile per environment is required")
		}
	}
	return nil
}

func secretTaskEnvironment(root, environment, branch string) []string {
	values := withoutEnvironment(os.Environ(), "PM_ENV", "PM_BRANCH", "MISE_CEILING_PATHS")
	return append(values, "PM_ENV="+environment, "PM_BRANCH="+branch, "MISE_CEILING_PATHS="+filepath.Dir(root))
}

func writePrivateSecretFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".secret-env-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

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
