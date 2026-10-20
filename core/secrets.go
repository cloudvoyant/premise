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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cloudvoyant/premise/core/providers"
)

// ProjectSecrets declares application keys and fnox profiles, never secret values.
type ProjectSecrets struct {
	Config   string            `yaml:"config"`
	Profiles map[string]string `yaml:"profiles"`
	Keys     []string          `yaml:"keys"`
	File     string            `yaml:"file,omitempty"`
}

// Public API -----------------------------------------------------------------

// LoadProjectSecrets reads declared keys from CI variables or local fnox profiles.
// It never prints secret values or provider diagnostics.
func LoadProjectSecrets(ctx context.Context, root, project, environment, branch string) (map[string]string, error) {
	if runningInCI() {
		if _, err := ResolveStackName(InfraOptions{Environment: environment, Branch: branch}); err != nil {
			return nil, err
		}
		manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
		if err != nil {
			return nil, err
		}
		for _, candidate := range manifest.Workspace.Projects {
			if candidate.Name != project {
				continue
			}
			if candidate.Secrets == nil {
				return nil, fmt.Errorf("project %s does not declare application secrets", project)
			}
			values := map[string]string{}
			for _, key := range candidate.Secrets.Keys {
				value := os.Getenv(key)
				if value == "" || !utf8.ValidString(value) {
					return nil, fmt.Errorf("missing or invalid CI application variable %s for %s; sync or configure CI secrets", key, project)
				}
				values[key] = value
			}
			return values, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrProjectNotFound, project)
	}
	return loadProjectSecrets(ctx, root, project, environment, branch, commandSecretReader{})
}

// PullProjectSecrets writes ignored application env files with user-only permissions.
// An empty project selects all projects declaring application secrets.
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
		values, err := loadProjectSecrets(ctx, root, candidate.Name, environment, branch, commandSecretReader{})
		if err != nil {
			return err
		}
		directory, err := TemplateDirectory(root, candidate.Path)
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
		pending = append(pending, pendingFile{path, encodeSecretEnv(values)})
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

// ExecProjectSecrets runs a command with application secrets in its child environment.
// Deployment administration credentials are removed before the child starts.
func ExecProjectSecrets(ctx context.Context, root, project, environment, branch string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("a command after -- is required")
	}
	values, err := LoadProjectSecrets(ctx, root, project, environment, branch)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	for _, candidate := range manifest.Workspace.Projects {
		if candidate.Name == project {
			command.Dir, err = TemplateDirectory(root, candidate.Path)
			if err != nil {
				return err
			}
		}
	}
	command.Env = appSecretEnvironment(os.Environ(), values)
	command.Stdin = stdin
	output := &secretRedactor{destination: stdout, values: values}
	diagnostics := &secretRedactor{destination: stderr, values: values}
	command.Stdout, command.Stderr = output, diagnostics
	runErr := command.Run()
	if err := errors.Join(output.Flush(), diagnostics.Flush()); err != nil {
		return err
	}
	if err := runErr; err != nil {
		return fmt.Errorf("application secret command failed: %w", err)
	}
	return nil
}

// SyncProjectSecrets copies local fnox values into explicitly selected CI secrets.
// Values travel on stdin and provider diagnostics remain private.
func SyncProjectSecrets(ctx context.Context, root, project, environment, branch, ci, repository string) error {
	if project == "" || repository == "" || strings.HasPrefix(repository, "-") || !strings.Contains(repository, "/") {
		return errors.New("secrets sync requires --project and --repository owner/repository")
	}
	if ci != "github" && ci != "gitlab" {
		return errors.New("secrets sync supports github or gitlab")
	}
	values, err := loadProjectSecrets(ctx, root, project, environment, branch, commandSecretReader{})
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := providers.SyncCISecret(ctx, root, ci, repository, environment, key, values[key]); err != nil {
			return err
		}
	}
	return nil
}

// Private helpers ------------------------------------------------------------

var secretKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var deploymentSecretKeys = providers.AdministrationSecretKeys()

type secretReader interface {
	Read(context.Context, string, string, string, []string) (map[string]string, error)
}
type commandSecretReader struct{}

func (commandSecretReader) Read(ctx context.Context, directory, config, profile string, keys []string) (map[string]string, error) {
	return (providers.Fnox{Environment: appSecretEnvironment(os.Environ(), nil)}).Read(ctx, directory, config, profile, keys)
}

func validateProjectSecrets(project Project) error {
	settings := project.Secrets
	if settings == nil {
		return nil
	}
	if settings.Config == "" || filepath.IsAbs(settings.Config) || escapesRoot(settings.Config) {
		return errors.New("secret config must be a path inside the application")
	}
	if len(settings.Keys) == 0 || len(settings.Profiles) == 0 {
		return errors.New("secret keys and environment profiles are required")
	}
	if settings.File != "" && (filepath.Base(settings.File) != settings.File || !strings.HasPrefix(settings.File, ".env") || settings.File == ".env.example") {
		return errors.New("secret file must be a local .env filename")
	}
	seen := map[string]bool{}
	for _, key := range settings.Keys {
		if !secretKeyPattern.MatchString(key) || deploymentSecretKeys[key] || strings.HasPrefix(key, "PM_") || strings.HasPrefix(key, "PULUMI_") || seen[key] {
			return fmt.Errorf("invalid, duplicate or deployment-only application secret key %q", key)
		}
		seen[key] = true
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

func loadProjectSecrets(ctx context.Context, root, name, environment, branch string, reader secretReader) (map[string]string, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	for _, project := range manifest.Workspace.Projects {
		if project.Name != name {
			continue
		}
		if project.Secrets == nil {
			return nil, fmt.Errorf("project %s does not declare application secrets", name)
		}
		if err := validateProjectSecrets(project); err != nil {
			return nil, err
		}
		profile := project.Secrets.Profiles[environment]
		if profile == "" {
			return nil, fmt.Errorf("project %s has no secret profile for %s", name, environment)
		}
		stack, err := ResolveStackName(InfraOptions{Environment: environment, Branch: branch})
		if err != nil {
			return nil, err
		}
		profile = strings.ReplaceAll(profile, "{branch}", strings.TrimPrefix(stack, "preview-"))
		directory, err := TemplateDirectory(root, project.Path)
		if err != nil {
			return nil, err
		}
		config := filepath.Join(directory, project.Secrets.Config)
		if err := rejectSymlinkComponents(directory, config); err != nil {
			return nil, err
		}
		resolved, err := reader.Read(ctx, directory, config, profile, project.Secrets.Keys)
		if err != nil {
			return nil, err
		}
		values := map[string]string{}
		for _, key := range project.Secrets.Keys {
			value := resolved[key]
			if value == "" || strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
				return nil, fmt.Errorf("secret %s must be nonempty UTF-8 without NUL", key)
			}
			values[key] = value
		}
		return values, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrProjectNotFound, name)
}

func encodeSecretEnv(values map[string]string) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&result, "%s=%s\n", key, strconv.Quote(values[key]))
	}
	return []byte(result.String())
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

func appSecretEnvironment(environment []string, values map[string]string) []string {
	var result []string
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !deploymentSecretKeys[key] && !strings.HasPrefix(key, "FNOX_") && key != "ENPASS_PASSWORD" && values[key] == "" {
			result = append(result, entry)
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
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
