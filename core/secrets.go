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

	"github.com/pelletier/go-toml/v2"
)

// ProjectSecrets declares application keys and fnox profiles, never secret values.
type ProjectSecrets struct {
	Config   string            `yaml:"config"`
	Profiles map[string]string `yaml:"profiles"`
	Keys     []string          `yaml:"keys"`
	File     string            `yaml:"file,omitempty"`
}

// Public API -----------------------------------------------------------------

// LoadProjectSecrets resolves only declared application keys through fnox and Mise.
// It does not print provider output or accept undeclared environment profiles.
func LoadProjectSecrets(ctx context.Context, root, project, environment, branch string) (map[string]string, error) {
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
		values, err := LoadProjectSecrets(ctx, root, candidate.Name, environment, branch)
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

// Private helpers ------------------------------------------------------------

var secretKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var deploymentSecretKeys = map[string]bool{
	"NEON_API_KEY": true, "NEON_API_TOKEN": true, "VERCEL_TOKEN": true,
	"VERCEL_API_TOKEN": true, "GH_TOKEN": true, "GITHUB_TOKEN": true,
	"GITLAB_TOKEN": true, "PULUMI_ACCESS_TOKEN": true,
	"PULUMI_CONFIG_PASSPHRASE": true,
}

type secretReader interface {
	Read(context.Context, string, string, string, string) (string, error)
}
type commandSecretReader struct{}

func (commandSecretReader) Read(ctx context.Context, directory, config, profile, key string) (string, error) {
	command := exec.CommandContext(ctx, "mise", "exec", "fnox@1.39.0", "--", "fnox", "--config", config, "--profile", profile, "--no-defaults", "--no-daemon", "--non-interactive", "--if-missing", "error", "get", key)
	command.Dir = directory
	// Avoid accidental profile/config overrides and verbose provider logs.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "FNOX_") && !strings.HasPrefix(entry, "RUST_LOG=") {
			command.Env = append(command.Env, entry)
		}
	}
	var output bytes.Buffer
	command.Stdout = &output
	// Provider diagnostics can contain credentials; report the key, never their text.
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("resolve application secret %s: provider failed", key)
	}
	return strings.TrimSuffix(output.String(), "\n"), nil
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
		if !secretKeyPattern.MatchString(key) || deploymentSecretKeys[key] || seen[key] {
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
		stack, err := InfraStack(InfraOptions{Environment: environment, Branch: branch})
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
		data, err := os.ReadFile(config)
		if err != nil {
			return nil, fmt.Errorf("read fnox configuration: %w", err)
		}
		var settings struct {
			Profiles map[string]struct {
				Secrets map[string]any `toml:"secrets"`
			} `toml:"profiles"`
		}
		if err := toml.Unmarshal(data, &settings); err != nil {
			return nil, errors.New("invalid fnox configuration")
		}
		selected, exists := settings.Profiles[profile]
		if !exists {
			return nil, fmt.Errorf("explicit fnox profile %q does not exist", profile)
		}
		values := map[string]string{}
		for _, key := range project.Secrets.Keys {
			if _, exists := selected.Secrets[key]; !exists {
				return nil, fmt.Errorf("secret %s is missing from profile %s", key, profile)
			}
			value, err := reader.Read(ctx, directory, config, profile, key)
			if err != nil {
				return nil, err
			}
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
		if !deploymentSecretKeys[key] && values[key] == "" {
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
