package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	ManifestFilename = "premise.yaml"
	SchemaVersion    = "0.2"
)

var (
	projectNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	ErrNotPremiseProject = errors.New("not a premise project")
)

// Config is the validated in-memory form of premise.yaml.
// WorkflowAsset is a client GitHub Actions workflow embedded in the CLI.
type WorkflowAsset struct {
	Path    string
	Content string
}

// WorkspaceScaffold contains the embedded files used to initialize a workspace.
type WorkspaceScaffold struct {
	MiseTemplate   string
	WorkflowAssets []WorkflowAsset
}

type Config struct {
	Workspace        Workspace         `yaml:"workspace"`
	TemplateRegistry *TemplateRegistry `yaml:"template_registry,omitempty"`
}

type TemplateRegistry struct {
	WorkspaceFiles []string   `yaml:"workspace_files"`
	Templates      []Template `yaml:"templates"`
}

type Workspace struct {
	Name            string      `yaml:"name"`
	Kind            ProjectKind `yaml:"kind,omitempty"`
	SchemaVersion   string      `yaml:"schema-version"`
	PackageManagers []string    `yaml:"package_managers,omitempty"`
	Providers       Providers   `yaml:"providers"`
	Projects        []Project   `yaml:"projects"`
}

type Providers struct {
	CI         string `yaml:"ci"`
	Tools      string `yaml:"tools"`
	Tasks      string `yaml:"tasks"`
	Infra      string `yaml:"infra"`
	Versioning string `yaml:"versioning"`
}

type PlatformDeclaration struct {
	CheckPlatforms   []string `yaml:"check_platforms,omitempty"`
	ReleasePlatforms []string `yaml:"release_platforms,omitempty"`
}

type Project struct {
	Name     string              `yaml:"name"`
	Template string              `yaml:"template"`
	Version  string              `yaml:"version,omitempty"`
	Path     string              `yaml:"path"`
	CI       PlatformDeclaration `yaml:"ci,omitempty"`
	Answers  map[string]string   `yaml:"answers,omitempty"`
}

type Template struct {
	Name          string              `yaml:"name"`
	Kind          string              `yaml:"kind"`
	Path          string              `yaml:"path"`
	Version       string              `yaml:"version,omitempty"`
	CI            PlatformDeclaration `yaml:"ci,omitempty"`
	Questions     []Question          `yaml:"questions"`
	Substitutions map[string]string   `yaml:"substitutions,omitempty"`
}

type Question struct {
	Prompt   string   `yaml:"prompt"`
	Type     string   `yaml:"type"`
	Populate string   `yaml:"populate"`
	Choices  []string `yaml:"choices,omitempty"`
}

func NewManifest(name string) Config {
	return Config{
		Workspace: Workspace{
			Name:          name,
			SchemaVersion: SchemaVersion,
			Providers: Providers{
				CI:         "github",
				Tools:      "mise",
				Tasks:      "mise",
				Infra:      "pulumi",
				Versioning: "svu",
			},
			Projects: []Project{},
		},
	}
}

func LoadManifest(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open manifest: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)

	var manifest Config
	if err := decoder.Decode(&manifest); err != nil {
		return Config{}, fmt.Errorf("decode manifest: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode manifest: multiple YAML documents are not supported")
		}
		return Config{}, fmt.Errorf("decode manifest: %w", err)
	}

	if manifest.Workspace.Projects == nil {
		manifest.Workspace.Projects = []Project{}
	}
	if manifest.TemplateRegistry != nil && manifest.TemplateRegistry.Templates == nil {
		manifest.TemplateRegistry.Templates = []Template{}
	}
	if err := manifest.Validate(); err != nil {
		return Config{}, err
	}
	return manifest, nil
}

func SaveManifest(path string, manifest Config) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(&manifest)
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".premise.yaml-*")
	if err != nil {
		return fmt.Errorf("create temporary manifest: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return fmt.Errorf("set manifest permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync manifest: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close manifest: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	return nil
}

// InitializeTemplateRegistry creates a Premise manifest and template directory
// without adding monorepo conventions.
func InitializeTemplateRegistry(root string) (string, error) {
	manifestPath := filepath.Join(root, ManifestFilename)
	if _, err := os.Lstat(manifestPath); err == nil {
		return "", fmt.Errorf("%s already exists", manifestPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect manifest: %w", err)
	}
	name := filepath.Base(filepath.Clean(root))
	if err := ValidateProjectName(name); err != nil {
		return "", fmt.Errorf("registry directory name: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		return "", fmt.Errorf("create templates directory: %w", err)
	}
	manifest := NewManifest(name)
	manifest.Workspace.Kind = ProjectKindTemplateRegistry
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{}}
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return "", err
	}
	return manifestPath, nil
}

func InitializeWorkspace(root, workspaceMiseTemplate string, workflowAssets ...WorkflowAsset) (string, error) {
	if strings.TrimSpace(workspaceMiseTemplate) == "" {
		return "", errors.New("workspace mise template is empty")
	}
	manifestPath := filepath.Join(root, ManifestFilename)
	if _, err := os.Lstat(manifestPath); err == nil {
		return "", fmt.Errorf("%s already exists", manifestPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect manifest: %w", err)
	}

	name := filepath.Base(filepath.Clean(root))
	if err := ValidateProjectName(name); err != nil {
		return "", fmt.Errorf("workspace directory name: %w", err)
	}
	if err := preflightWorkspace(root, workflowAssets); err != nil {
		return "", err
	}
	for _, directory := range []string{"apps", "libs"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			return "", fmt.Errorf("create %s directory: %w", directory, err)
		}
	}
	if err := ensureWorkspaceMise(root, workspaceMiseTemplate); err != nil {
		return "", err
	}
	if err := ensureWorkflowAssets(root, workflowAssets); err != nil {
		return "", err
	}
	manifest := NewManifest(name)
	manifest.Workspace.Kind = ProjectKindMonorepo
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return "", err
	}
	return manifestPath, nil
}

func preflightWorkspace(root string, assets []WorkflowAsset) error {
	for _, path := range []string{
		filepath.Join(root, ManifestFilename),
		filepath.Join(root, "apps"),
		filepath.Join(root, "libs"),
		filepath.Join(root, "mise.toml"),
	} {
		if err := rejectSymlinkComponents(root, path); err != nil {
			return err
		}
	}
	for _, directory := range []string{"apps", "libs"} {
		path := filepath.Join(root, directory)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return fmt.Errorf("workspace %s is not a directory", path)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect workspace %s: %w", path, err)
		}
	}
	misePath := filepath.Join(root, "mise.toml")
	if info, err := os.Stat(misePath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("workspace mise config %s is a directory", misePath)
		}
		monorepo, err := hasMonorepoRoot(root)
		if err != nil {
			return fmt.Errorf("validate existing workspace mise config: %w", err)
		}
		if !monorepo {
			return errors.New("existing mise.toml must declare top-level monorepo_root = true")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workspace mise config: %w", err)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)
	seen := make(map[string]struct{}, len(assets))
	for _, asset := range assets {
		if strings.TrimSpace(asset.Path) == "" {
			return errors.New("workflow asset path is empty")
		}
		path := filepath.Clean(filepath.Join(root, asset.Path))
		if err := rejectSymlinkComponents(root, path); err != nil {
			return err
		}
		pathAbs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve workflow %s: %w", path, err)
		}
		if pathAbs != rootAbs && !strings.HasPrefix(pathAbs, rootAbs+string(filepath.Separator)) {
			return fmt.Errorf("workflow %s is outside workspace root", asset.Path)
		}
		if _, ok := seen[pathAbs]; ok {
			return fmt.Errorf("workflow %s is duplicated", path)
		}
		seen[pathAbs] = struct{}{}
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("workflow %s already exists", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect workflow %s: %w", path, err)
		}
	}
	return nil
}

func rejectSymlinkComponents(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	rel, err := filepath.Rel(filepath.Clean(rootAbs), filepath.Clean(pathAbs))
	if err != nil {
		return fmt.Errorf("compare workspace path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workspace path %s is outside workspace root", path)
	}
	current := filepath.Clean(rootAbs)
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect workspace path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace path %s contains a symlink", path)
		}
	}
	return nil
}

func ensureWorkflowAssets(root string, assets []WorkflowAsset) error {
	if len(assets) == 0 {
		return nil
	}
	for _, asset := range assets {
		if strings.TrimSpace(asset.Path) == "" {
			return errors.New("workflow asset path is empty")
		}
		path := filepath.Join(root, asset.Path)
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("workflow %s already exists", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect workflow %s: %w", path, err)
		}
	}
	for _, asset := range assets {
		path := filepath.Join(root, asset.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create workflow directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(asset.Content), 0o644); err != nil {
			return fmt.Errorf("write workflow %s: %w", path, err)
		}
	}
	return nil
}

func ensureWorkspaceMise(root, workspaceMiseTemplate string) error {
	path := filepath.Join(root, "mise.toml")
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("workspace mise config %s is a directory", path)
		}
		monorepo, err := hasMonorepoRoot(root)
		if err != nil {
			return fmt.Errorf("validate existing workspace mise config: %w", err)
		}
		if !monorepo {
			return errors.New("existing mise.toml must declare top-level monorepo_root = true")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect workspace mise config: %w", err)
	}
	if err := os.WriteFile(path, []byte(workspaceMiseTemplate), 0o644); err != nil {
		return fmt.Errorf("write workspace mise config: %w", err)
	}
	return nil
}

// HasProjectMiseConfigs reports whether the conventional app or library roots
// contain at least one project-level mise.toml file.
func HasProjectMiseConfigs(root string) (bool, error) {
	for _, directory := range []string{"apps", "libs"} {
		entries, err := os.ReadDir(filepath.Join(root, directory))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("read %s projects: %w", directory, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			configPath := filepath.Join(root, directory, entry.Name(), "mise.toml")
			info, err := os.Stat(configPath)
			if err == nil && !info.IsDir() {
				return true, nil
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return false, fmt.Errorf("inspect project mise config %s: %w", configPath, err)
			}
		}
	}
	return false, nil
}

func FindManifest(start string) (string, string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", "", fmt.Errorf("resolve current directory: %w", err)
	}
	for {
		path := filepath.Join(current, ManifestFilename)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return current, path, nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("inspect %s: %w", path, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", fmt.Errorf("%w: %s not found from %s", ErrNotPremiseProject, ManifestFilename, start)
		}
		current = parent
	}
}

func (manifest Config) Validate() error {
	if strings.TrimSpace(manifest.Workspace.Name) == "" {
		return errors.New("workspace.name is required")
	}
	if manifest.Workspace.SchemaVersion != SchemaVersion {
		return fmt.Errorf("workspace.schema-version must be %q", SchemaVersion)
	}
	seenPackageManagers := make(map[string]struct{}, len(manifest.Workspace.PackageManagers))
	for index, packageManager := range manifest.Workspace.PackageManagers {
		if err := ValidateProjectName(packageManager); err != nil {
			return fmt.Errorf("workspace.package_managers[%d]: %w", index, err)
		}
		if _, duplicate := seenPackageManagers[packageManager]; duplicate {
			return fmt.Errorf("workspace.package_managers contains duplicate %q", packageManager)
		}
		seenPackageManagers[packageManager] = struct{}{}
	}

	switch manifest.Workspace.Kind {
	case "", ProjectKindMonorepo:
	case ProjectKindTemplateRegistry:
		if manifest.TemplateRegistry == nil {
			return errors.New("template-registry workspace requires template_registry configuration")
		}
	default:
		return fmt.Errorf("workspace.kind must be %q or %q", ProjectKindMonorepo, ProjectKindTemplateRegistry)
	}

	var templates []Template
	if manifest.TemplateRegistry != nil {
		if manifest.TemplateRegistry.WorkspaceFiles == nil {
			return errors.New("template_registry.workspace_files is required; use [] when no workspace files are shared")
		}
		seenPatterns := make(map[string]struct{}, len(manifest.TemplateRegistry.WorkspaceFiles))
		for index, pattern := range manifest.TemplateRegistry.WorkspaceFiles {
			if err := validateWorkspaceFilePattern(pattern); err != nil {
				return fmt.Errorf("template_registry.workspace_files[%d]: %w", index, err)
			}
			if _, duplicate := seenPatterns[pattern]; duplicate {
				return fmt.Errorf("template_registry.workspace_files contains duplicate pattern %q", pattern)
			}
			seenPatterns[pattern] = struct{}{}
		}
		templates = manifest.TemplateRegistry.Templates
	}

	templateNames := map[string]struct{}{}
	templatePaths := map[string]struct{}{}
	for index, template := range templates {
		if err := ValidateTemplateName(template.Name); err != nil {
			return fmt.Errorf("template_registry.templates[%d].name: %w", index, err)
		}
		if _, exists := templateNames[template.Name]; exists {
			return fmt.Errorf("duplicate template name %q", template.Name)
		}
		if _, err := KindDirectory(template.Kind); err != nil {
			return fmt.Errorf("template_registry.templates[%d]: %w", index, err)
		}
		if err := validateTemplatePath(template.Path); err != nil {
			return fmt.Errorf("template_registry.templates[%d].path: %w", index, err)
		}
		if err := ValidatePlatformDeclaration(template.CI); err != nil {
			return fmt.Errorf("template_registry.templates[%d].ci: %w", index, err)
		}
		if _, exists := templatePaths[template.Path]; exists {
			return fmt.Errorf("duplicate template path %q", template.Path)
		}
		templateNames[template.Name] = struct{}{}
		templatePaths[template.Path] = struct{}{}

		answers := map[string]struct{}{}
		for questionIndex, question := range template.Questions {
			if strings.TrimSpace(question.Prompt) == "" {
				return fmt.Errorf("template_registry.templates[%d].questions[%d].prompt is required", index, questionIndex)
			}
			if strings.TrimSpace(question.Populate) == "" {
				return fmt.Errorf("template_registry.templates[%d].questions[%d].populate is required", index, questionIndex)
			}
			if _, exists := answers[question.Populate]; exists {
				return fmt.Errorf("template %q has duplicate answer %q", template.Name, question.Populate)
			}
			switch question.Type {
			case "string":
				if len(question.Choices) != 0 {
					return fmt.Errorf("template %q string question %q cannot define choices", template.Name, question.Populate)
				}
			case "radio", "select":
				if len(question.Choices) == 0 {
					return fmt.Errorf("template %q question %q requires choices", template.Name, question.Populate)
				}
			default:
				return fmt.Errorf("template %q question %q has unsupported type %q", template.Name, question.Populate, question.Type)
			}
			answers[question.Populate] = struct{}{}
		}
		if _, exists := answers["name"]; !exists {
			return fmt.Errorf("template %q must populate the name answer", template.Name)
		}
		for token, answer := range template.Substitutions {
			if token == "" {
				return fmt.Errorf("template %q has an empty substitution token", template.Name)
			}
			if _, exists := answers[answer]; !exists {
				return fmt.Errorf("template %q substitution %q references unknown answer %q", template.Name, token, answer)
			}
		}
	}

	projectNames := map[string]struct{}{}
	projectPaths := map[string]struct{}{}
	for index, project := range manifest.Workspace.Projects {
		if err := ValidateProjectName(project.Name); err != nil {
			return fmt.Errorf("workspace.projects[%d].name: %w", index, err)
		}
		if project.Template == "" {
			return fmt.Errorf("workspace.projects[%d].template is required", index)
		}
		if project.Path == "" || filepath.IsAbs(project.Path) || escapesRoot(project.Path) {
			return fmt.Errorf("workspace.projects[%d].path must be a relative path inside the workspace", index)
		}
		if _, exists := projectNames[project.Name]; exists {
			return fmt.Errorf("duplicate project name %q", project.Name)
		}
		if _, exists := projectPaths[project.Path]; exists {
			return fmt.Errorf("duplicate project path %q", project.Path)
		}
		if err := ValidatePlatformDeclaration(project.CI); err != nil {
			return fmt.Errorf("workspace.projects[%d].ci: %w", index, err)
		}
		projectNames[project.Name] = struct{}{}
		projectPaths[project.Path] = struct{}{}
	}
	return nil
}

var validPlatforms = map[string]struct{}{"linux": {}, "macos": {}, "windows": {}}

func ValidatePlatformDeclaration(declaration PlatformDeclaration) error {
	seen := map[string]struct{}{}
	for field, values := range map[string][]string{"check_platforms": declaration.CheckPlatforms, "release_platforms": declaration.ReleasePlatforms} {
		for _, value := range values {
			if _, ok := validPlatforms[value]; !ok {
				return fmt.Errorf("%s contains unknown platform %q", field, value)
			}
			if _, ok := seen[field+":"+value]; ok {
				return fmt.Errorf("%s contains duplicate platform %q", field, value)
			}
			seen[field+":"+value] = struct{}{}
		}
	}
	return nil
}

func (manifest Config) DeclaredTemplates() []Template {
	if manifest.TemplateRegistry == nil {
		return nil
	}
	return manifest.TemplateRegistry.Templates
}

// PublicationTargets returns the templates that represent publishable packages
// for the configured workspace lifecycle. Registry workspaces publish their
// declared templates; monorepos publish their registered projects. The
// workspace kind is authoritative when both forms are present.
func (manifest Config) PublicationTargets() []Template {
	var targets []Template
	switch manifest.Workspace.Kind {
	case ProjectKindTemplateRegistry:
		targets = append(targets, manifest.DeclaredTemplates()...)
	default:
		targets = make([]Template, 0, len(manifest.Workspace.Projects))
		for _, project := range manifest.Workspace.Projects {
			kind := ""
			if strings.HasPrefix(filepath.ToSlash(project.Path), "apps/") {
				kind = "app"
			} else if strings.HasPrefix(filepath.ToSlash(project.Path), "libs/") {
				kind = "lib"
			}
			targets = append(targets, Template{Name: project.Name, Kind: kind, Path: project.Path, Version: project.Version})
		}
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Path == targets[j].Path {
			return targets[i].Name < targets[j].Name
		}
		return targets[i].Path < targets[j].Path
	})
	return targets
}

func (manifest Config) FindTemplate(name string) (Template, error) {
	for _, template := range manifest.DeclaredTemplates() {
		if template.Name == name {
			return template, nil
		}
	}
	return Template{}, fmt.Errorf("template %q is not declared", name)
}

// MergePackageManagers appends package-manager IDs that are not already
// declared, preserving both the existing order and the order of incoming IDs.
func (manifest *Config) MergePackageManagers(packageManagers ...string) error {
	seen := make(map[string]struct{}, len(manifest.Workspace.PackageManagers)+len(packageManagers))
	for _, packageManager := range manifest.Workspace.PackageManagers {
		seen[packageManager] = struct{}{}
	}
	for _, packageManager := range packageManagers {
		if _, exists := seen[packageManager]; exists {
			continue
		}
		manifest.Workspace.PackageManagers = append(manifest.Workspace.PackageManagers, packageManager)
		seen[packageManager] = struct{}{}
	}
	return manifest.Validate()
}

func (manifest *Config) AddProject(project Project) error {
	for _, existing := range manifest.Workspace.Projects {
		if existing.Name == project.Name {
			return fmt.Errorf("project %q is already registered", project.Name)
		}
		if existing.Path == project.Path {
			return fmt.Errorf("project path %q is already registered", project.Path)
		}
	}
	manifest.Workspace.Projects = append(manifest.Workspace.Projects, project)
	sort.Slice(manifest.Workspace.Projects, func(i, j int) bool {
		return manifest.Workspace.Projects[i].Path < manifest.Workspace.Projects[j].Path
	})
	return manifest.Validate()
}

func validateTemplatePath(templatePath string) error {
	if strings.TrimSpace(templatePath) == "" {
		return errors.New("path is required")
	}
	if filepath.IsAbs(templatePath) || strings.Contains(templatePath, `\`) {
		return errors.New("path must be relative to the registry root and use forward slashes")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(templatePath)))
	if clean != templatePath || clean == "." || escapesRoot(clean) {
		return errors.New("path must be a normalized directory below the registry root")
	}
	return nil
}

func validateWorkspaceFilePattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return errors.New("pattern is required")
	}
	if filepath.IsAbs(pattern) || strings.ContainsAny(pattern, `/\\`) || pattern == "." || pattern == ".." {
		return errors.New("pattern must match direct files in the registry root")
	}
	if _, err := filepath.Match(pattern, "workspace-file"); err != nil {
		return fmt.Errorf("invalid glob %q: %w", pattern, err)
	}
	if pattern == ManifestFilename {
		return fmt.Errorf("%s is registry metadata and cannot be copied", ManifestFilename)
	}
	return nil
}

func ValidateProjectName(name string) error {
	if !projectNamePattern.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("%q must start with a letter or number and contain only letters, numbers, dots, underscores, or hyphens", name)
	}
	return nil
}

func ValidateTemplateName(name string) error {
	if err := ValidateProjectName(name); err != nil {
		return fmt.Errorf("invalid template name: %w", err)
	}
	return nil
}

func KindDirectory(kind string) (string, error) {
	switch kind {
	case "app":
		return "apps", nil
	case "lib":
		return "libs", nil
	default:
		return "", fmt.Errorf("unsupported template kind %q", kind)
	}
}

func ResolveSubstitutions(template Template, answers map[string]string) (map[string]string, error) {
	replacements := make(map[string]string, len(template.Substitutions))
	for token, answerName := range template.Substitutions {
		value, exists := answers[answerName]
		if !exists {
			return nil, fmt.Errorf("substitution %q requires answer %q", token, answerName)
		}
		replacements[token] = value
	}
	return replacements, nil
}

func escapesRoot(path string) bool {
	clean := filepath.Clean(path)
	return clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
