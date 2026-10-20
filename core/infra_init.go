package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// InfrastructureSetup selects an empty language starter or adopts existing code.
// PulumiVersion is persisted only in the workspace mise.toml.
type InfrastructureSetup struct {
	Language      string
	PulumiVersion string
}

// Public API -----------------------------------------------------------------

// InitializeWorkspaceWithInfrastructure stages workspace files and Pulumi code
// before writing to the destination. Existing infrastructure is preserved.
func InitializeWorkspaceWithInfrastructure(ctx context.Context, root string, scaffold WorkspaceScaffold, setup InfrastructureSetup, stdin io.Reader, stdout, stderr io.Writer) (string, error) {
	if _, err := os.Lstat(filepath.Join(root, ManifestFilename)); err == nil {
		return "", fmt.Errorf("%s already exists", ManifestFilename)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := preflightWorkspace(root, scaffold.WorkflowAssets); err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(root, filepath.Join(root, "infra")); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp("", "premise-init-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	originalMise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if originalMise != nil {
		if err := os.WriteFile(filepath.Join(stage, "mise.toml"), originalMise, 0o644); err != nil {
			return "", err
		}
	}
	if _, err := InitializeWorkspace(stage, scaffold.MiseTemplate, scaffold.WorkflowAssets...); err != nil {
		return "", err
	}

	language, exists, err := existingPulumiLanguage(filepath.Join(root, "infra", "Pulumi.yaml"))
	if err != nil {
		return "", err
	}
	if exists {
		if setup.Language != "" && pulumiRuntimeName(setup.Language) != pulumiRuntimeName(language) {
			return "", fmt.Errorf("infra/Pulumi.yaml uses %s; --infra-language %s would change the existing program", language, setup.Language)
		}
	} else {
		if setup.Language == "" {
			return "", fmt.Errorf("choose --infra-language for a new Pulumi project")
		}
		language, err = pulumiLanguageLabel(setup.Language)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(root, "infra")); err == nil {
			return "", fmt.Errorf("infra/ already exists without Pulumi.yaml; initialize that project before adopting it")
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := configureInfrastructureTools(filepath.Join(stage, "mise.toml"), language, setup.PulumiVersion); err != nil {
		return "", err
	}
	if !exists {
		home, err := os.MkdirTemp("", "premise-pulumi-bootstrap-*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(home)
		runner := miseRunner{
			Stdin: stdin, Stdout: stdout, Stderr: stderr,
			RemoveEnvironment: []string{"PULUMI_ACCESS_TOKEN", "PULUMI_BACKEND_URL"},
		}
		starter := "https://github.com/pulumi/templates/tree/master/" + language
		if err := runner.run(ctx, stage, []string{"MISE_TRUSTED_CONFIG_PATHS=" + stage, "PULUMI_HOME=" + home}, "exec", "--", "pulumi", "new", starter,
			"--generate-only", "--non-interactive", "--yes", "--name", filepath.Base(root),
			"--dir", filepath.Join(stage, "infra")); err != nil {
			return "", fmt.Errorf("initialize %s Pulumi project: %w", language, err)
		}
	}
	manifest := NewManifest(filepath.Base(root))
	manifest.Workspace.Kind = ProjectKindMonorepo
	manifest.Workspace.Infra = &Infrastructure{
		Directory: "infra",
		Environments: map[string]InfraEnvironment{
			"dev": {}, "stage": {}, "prod": {}, "preview": {},
		},
	}
	if err := SaveManifest(filepath.Join(stage, ManifestFilename), manifest); err != nil {
		return "", err
	}
	if err := commitInfrastructureWorkspace(root, stage, originalMise); err != nil {
		return "", err
	}
	return filepath.Join(root, ManifestFilename), nil
}

// Private helpers ------------------------------------------------------------

const defaultPulumiVersion = "3.268.0"

func existingPulumiLanguage(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var project struct {
		Runtime any `yaml:"runtime"`
	}
	if err := yaml.Unmarshal(data, &project); err != nil {
		return "", false, fmt.Errorf("read existing Pulumi runtime: %w", err)
	}
	name, _ := project.Runtime.(string)
	if runtime, ok := project.Runtime.(map[string]any); ok {
		name, _ = runtime["name"].(string)
	}
	if name == "" {
		return "", false, fmt.Errorf("%s must declare a Pulumi runtime", path)
	}
	return name, true, nil
}

func pulumiRuntimeName(language string) string {
	switch language {
	case "typescript", "javascript":
		return "nodejs"
	case "csharp", "fsharp":
		return "dotnet"
	default:
		return language
	}
}

func configureInfrastructureTools(path, language, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	configuration, err := ExtractMiseConfig(path, data)
	if err != nil {
		return err
	}
	tools, ok := configuration.Root["tools"].(map[string]any)
	if !ok {
		tools = map[string]any{}
		configuration.Root["tools"] = tools
	}
	if version != "" {
		tools["pulumi"] = version
	} else if _, exists := tools["pulumi"]; !exists {
		tools["pulumi"] = defaultPulumiVersion
	}
	tool, fallback := "", ""
	switch language {
	case "go":
		tool, fallback = "go", "1.25.8"
	case "typescript", "javascript", "nodejs":
		tool, fallback = "node", "24"
	case "python":
		tool, fallback = "python", "3.13"
	}
	if tool != "" {
		if _, exists := tools[tool]; !exists {
			tools[tool] = fallback
		}
	}
	tasks, ok := configuration.Root["tasks"].(map[string]any)
	if !ok {
		tasks = map[string]any{}
		configuration.Root["tasks"] = tasks
	}
	if _, exists := tasks["infra:install"]; !exists {
		tasks["infra:install"] = map[string]any{"dir": "infra", "run": "mise exec -- pulumi install"}
	}
	encoded, err := encodeMiseConfig(configuration)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

func commitInfrastructureWorkspace(root, stage string, originalMise []byte) error {
	var files []string
	if err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Pulumi starter contains a non-regular file: %s", path)
		}
		relative, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(root, relative)
		if err := rejectSymlinkComponents(root, destination); err != nil {
			return err
		}
		if _, err := os.Lstat(destination); err == nil && relative != "mise.toml" {
			return fmt.Errorf("workspace file %s already exists", relative)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		files = append(files, relative)
		return nil
	}); err != nil {
		return err
	}
	var created []string
	completed := false
	defer func() {
		if !completed {
			for _, path := range created {
				os.Remove(path)
			}
			if originalMise != nil {
				os.WriteFile(filepath.Join(root, "mise.toml"), originalMise, 0o644)
			}
		}
	}()
	for _, relative := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(stage, relative))
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
		if relative != "mise.toml" || originalMise == nil {
			created = append(created, path)
		}
	}
	for _, directory := range []string{"apps", "libs"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			return err
		}
	}
	completed = true
	return nil
}

// pulumiLanguageLabel normalizes a template selection without accepting template URLs.
func pulumiLanguageLabel(language string) (string, error) {
	language = strings.TrimSpace(language)
	switch language {
	case "go", "typescript", "javascript", "python", "yaml", "csharp", "fsharp", "java":
		return language, nil
	default:
		return "", fmt.Errorf("--infra-language must name an empty Pulumi language starter: go, typescript, javascript, python, yaml, csharp, fsharp, or java")
	}
}
