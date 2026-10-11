package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Public API -----------------------------------------------------------------

// BindInfra binds an app once in project-level Pulumi configuration, inherited
// by every current and future stack. Shared classifies the target project scope.
func BindInfra(root, app, infrastructure, artifact string, shared bool) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	projects, err := infrastructureProjects(root, infrastructure, false, false)
	if err != nil {
		return err
	}
	if len(projects) != 1 {
		return fmt.Errorf("unknown infrastructure project %q", infrastructure)
	}
	if isSharedInfraProject(projects[0]) && !shared {
		return errors.New("shared infrastructure bindings require --shared")
	}
	if artifact == "" || filepath.Clean(artifact) == "." || filepath.IsAbs(artifact) || escapesRoot(artifact) {
		return errors.New("artifact must be a subdirectory relative to the app")
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	var appProject Project
	for _, project := range manifest.Workspace.Projects {
		if project.Name == app && strings.HasPrefix(project.Path, "apps/") {
			appProject = project
			break
		}
	}
	if appProject.Path == "" {
		return fmt.Errorf("%w: app %s", ErrProjectNotFound, app)
	}
	directory, err := TemplateDirectory(root, projects[0].Path)
	if err != nil {
		return err
	}
	if shared && !isSharedInfraProject(projects[0]) {
		settings, err := filepath.Glob(filepath.Join(directory, "Pulumi.*.yaml"))
		if err != nil {
			return err
		}
		for _, path := range settings {
			if filepath.Base(path) != "Pulumi.shared.yaml" {
				return errors.New("cannot change initialized environment infrastructure to shared; migrate its stacks explicitly")
			}
		}
	}
	contractPath := filepath.Join(directory, "premise-infra.yaml")
	if err := rejectSymlinkComponents(root, contractPath); err != nil {
		return err
	}
	data, err := os.ReadFile(contractPath)
	if err != nil {
		return fmt.Errorf("read template binding contract: %w", err)
	}
	var contract infraBindingContract
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&contract); err != nil {
		return fmt.Errorf("decode template binding contract: %w", err)
	}
	if strings.TrimSpace(contract.ArtifactConfigKey) == "" {
		return errors.New("template must declare artifact_config_key")
	}
	appDirectory, err := TemplateDirectory(root, appProject.Path)
	if err != nil {
		return err
	}
	artifactPath, err := resolveFuturePath(filepath.Join(appDirectory, artifact))
	if err != nil || !inside(appDirectory, artifactPath) {
		return errors.New("artifact path escapes app directory")
	}
	relative, err := filepath.Rel(directory, artifactPath)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "Pulumi.yaml")
	if err := rejectSymlinkComponents(root, path); err != nil {
		return err
	}
	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("decode Pulumi project configuration: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	configuration, err := infraConfigMap(settings, "config")
	if err != nil {
		return err
	}
	projectName, ok := settings["name"].(string)
	if !ok || projectName == "" {
		return errors.New("Pulumi.yaml must declare the project name")
	}
	key := strings.TrimPrefix(contract.ArtifactConfigKey, projectName+":")
	declaration, err := infraConfigMap(configuration, key)
	if err != nil {
		return err
	}
	bindings, err := infraConfigMap(declaration, "value")
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	if existing, found := bindings[app]; found && existing != relative {
		return fmt.Errorf("app %q already has a different binding in %s", app, path)
	}
	bindings[app] = relative
	data, err = yaml.Marshal(settings)
	if err != nil {
		return err
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := writeInfraSettings(path, data); err != nil {
		return err
	}
	if shared && (projects[0].Infra == nil || !projects[0].Infra.Shared) {
		for index := range manifest.Workspace.Projects {
			if manifest.Workspace.Projects[index].Name == infrastructure {
				manifest.Workspace.Projects[index].Infra = &InfraProjectConfig{Shared: true}
			}
		}
		if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
			return errors.Join(err, writeInfraSettings(path, original))
		}
	}
	return nil
}

// Private helpers ------------------------------------------------------------

type infraBindingContract struct {
	ArtifactConfigKey string `yaml:"artifact_config_key"`
}

func infraConfigMap(parent map[string]any, key string) (map[string]any, error) {
	if value, found := parent[key]; found {
		mapping, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Pulumi project config %q must be a mapping", key)
		}
		return mapping, nil
	}
	mapping := map[string]any{}
	parent[key] = mapping
	return mapping, nil
}

func writeInfraSettings(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pulumi-settings-*")
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
