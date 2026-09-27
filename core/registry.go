package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

// Configuration --------------------------------------------------------------

// OfficialSources is VCS URLs for premise-owned / first-party template registries.
var OfficialSources = []string{
	NativeTemplateSource,
	"cloudvoyant/premise-cargo",
	"cloudvoyant/premise-bun",
}

// Public types ---------------------------------------------------------------

// TemplateSelector is a parsed template selector. Source is the registry
// identity, local path, or URL; Name is the template name; Local reports
// whether Source refers to a filesystem path.
type TemplateSelector struct {
	Source string
	Name   string
	Local  bool
}

// GenerateSelectorKind identifies how a generate argument should be resolved.
type GenerateSelectorKind int

const (
	GenerateSelectorExplicit GenerateSelectorKind = iota
	GenerateSelectorSource
	GenerateSelectorOfficialName
	GenerateSelectorDefault
)

// ClassifiedGenerateSelector carries a selector strategy and its value.
type ClassifiedGenerateSelector struct {
	Kind  GenerateSelectorKind
	Value string
}

// ProjectKind identifies the one lifecycle model owned by a Premise root.
type ProjectKind string

const (
	ProjectKindMonorepo         ProjectKind = "monorepo"
	ProjectKindTemplateRegistry ProjectKind = "template-registry"
)

// Registry lists templates available from one source.
type Registry struct {
	Templates []Template
}

// RegistryEntry carries one template from one registry source: the source
// repository identity, the declared template metadata, a human-readable display
// label, and a fully qualified selector.
type RegistryEntry struct {
	Source   string
	Template Template
	Label    string
}

// Public API -----------------------------------------------------------------

// ParseTemplateSelector splits a selector into its source and template name.
func ParseTemplateSelector(selector string) (TemplateSelector, error) {
	separator := strings.LastIndex(selector, ":")
	if separator < 0 || separator == len(selector)-1 {
		return TemplateSelector{}, fmt.Errorf("template selector %q must end with :<template>", selector)
	}
	source := selector[:separator]
	name := selector[separator+1:]
	if err := ValidateTemplateName(name); err != nil {
		return TemplateSelector{}, err
	}
	if source == "" {
		source = NativeTemplateSource
	}
	local := source == "." || filepath.IsAbs(source) || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
	return TemplateSelector{Source: source, Name: name, Local: local}, nil
}

// ClassifyGenerateSelector parses one generate argument for registry dispatch.
func ClassifyGenerateSelector(argument string) (ClassifiedGenerateSelector, error) {
	if argument == "" {
		return ClassifiedGenerateSelector{Kind: GenerateSelectorDefault}, nil
	}
	if name, ok := strings.CutPrefix(argument, ":"); ok {
		if err := ValidateTemplateName(name); err != nil {
			return ClassifiedGenerateSelector{}, err
		}
		return ClassifiedGenerateSelector{Kind: GenerateSelectorOfficialName, Value: name}, nil
	}
	if !strings.Contains(argument, ":") {
		return ClassifiedGenerateSelector{Kind: GenerateSelectorSource, Value: argument}, nil
	}
	if _, err := ParseTemplateSelector(argument); err != nil {
		return ClassifiedGenerateSelector{}, err
	}
	return ClassifiedGenerateSelector{Kind: GenerateSelectorExplicit, Value: argument}, nil
}

// DetectProjectKind selects the root's configured CI lifecycle without
// restricting which workspace and template-registry capabilities may coexist.
func DetectProjectKind(root string) (ProjectKind, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return "", err
	}
	hasMonorepo, err := hasMonorepoRoot(root)
	if err != nil {
		return "", err
	}
	hasRegistry := manifest.TemplateRegistry != nil
	switch manifest.Workspace.Kind {
	case ProjectKindMonorepo:
		if !hasMonorepo {
			return "", errors.New("monorepo project requires top-level monorepo_root = true in mise.toml")
		}
		return ProjectKindMonorepo, nil
	case ProjectKindTemplateRegistry:
		if !hasRegistry {
			return "", errors.New("template-registry project requires template_registry configuration")
		}
		return ProjectKindTemplateRegistry, nil
	case "":
		switch {
		case hasMonorepo:
			return ProjectKindMonorepo, nil
		case hasRegistry:
			return ProjectKindTemplateRegistry, nil
		default:
			return "", errors.New("premise project must configure a monorepo or template_registry")
		}
	default:
		return "", fmt.Errorf("unsupported project kind %q", manifest.Workspace.Kind)
	}
}

// Selector returns the fully qualified <source>:<template-name> selector for
// the entry. Selectors never rely on display labels, so duplicate template
// names across sources stay unambiguous.
func (entry RegistryEntry) Selector() string {
	return entry.Source + ":" + entry.Template.Name
}

func LoadRegistry(sourceRoot string) (Registry, error) {
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return Registry{}, fmt.Errorf("resolve registry root: %w", err)
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return Registry{}, fmt.Errorf("load registry manifest: %w", err)
	}
	if manifest.TemplateRegistry == nil {
		return Registry{}, errors.New("manifest does not configure template_registry")
	}
	templates := append([]Template{}, manifest.DeclaredTemplates()...)
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})
	return Registry{Templates: templates}, nil
}

// DefaultRegistry loads every official registry source and returns a merged,
// deterministically ordered list of source-aware entries. Any source that fails
// to load aborts the merge with a contextual aggregate error.
func DefaultRegistry(ctx context.Context) ([]RegistryEntry, error) {
	var entries []RegistryEntry
	var errs []error
	for _, source := range OfficialSources {
		sourceEntries, err := loadSourceEntries(ctx, source)
		if err != nil {
			errs = append(errs, fmt.Errorf("load official registry %s: %w", source, officialRegistryError(err)))
			continue
		}
		entries = append(entries, sourceEntries...)
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	disambiguateLabels(entries)
	return entries, nil
}

// ResolveOfficialTemplateName resolves a bare template name against every
// official registry and returns the single matching fully qualified selector.
// Zero matches or multiple matches return an error asking the caller to qualify
// the source. A bare name is never resolved against unofficial registries.
func ResolveOfficialTemplateName(ctx context.Context, name string) (string, error) {
	if err := ValidateTemplateName(name); err != nil {
		return "", err
	}
	var matches []RegistryEntry
	var errs []error
	for _, source := range OfficialSources {
		entries, err := loadSourceEntries(ctx, source)
		if err != nil {
			errs = append(errs, fmt.Errorf("load official registry %s: %w", source, officialRegistryError(err)))
			continue
		}
		for _, entry := range entries {
			if entry.Template.Name == name {
				matches = append(matches, entry)
			}
		}
	}
	if len(errs) > 0 {
		return "", errors.Join(errs...)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("template name %q is not declared by any official registry (%s); qualify the source as <owner>/<repo>:<name>", name, strings.Join(OfficialSources, ", "))
	case 1:
		return matches[0].Selector(), nil
	default:
		sources := make([]string, len(matches))
		for index, match := range matches {
			sources[index] = match.Source
		}
		return "", fmt.Errorf("template name %q is declared by multiple official registries (%s); qualify the source, e.g. %s:%s", name, strings.Join(sources, ", "), sources[0], name)
	}
}

// Names returns the template names declared by the registry.
func (registry Registry) Names() []string {
	names := make([]string, len(registry.Templates))
	for index, template := range registry.Templates {
		names[index] = template.Name
	}
	return names
}

// Private helpers ------------------------------------------------------------

func hasMonorepoRoot(root string) (bool, error) {
	mise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read mise config: %w", err)
	}
	var config struct {
		MonorepoRoot bool `toml:"monorepo_root"`
	}
	if err := toml.Unmarshal(mise, &config); err != nil {
		return false, fmt.Errorf("parse mise config: %w", err)
	}
	return config.MonorepoRoot, nil
}

// loadSourceEntries loads one source's registry and returns its entries with
// the source identity attached.
func loadSourceEntries(ctx context.Context, source string) ([]RegistryEntry, error) {
	root, err := resolveRepository(ctx, source)
	if err != nil {
		return nil, err
	}
	registry, err := LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	entries := make([]RegistryEntry, len(registry.Templates))
	for index, template := range registry.Templates {
		entries[index] = newRegistryEntry(source, template)
	}
	return entries, nil
}

func officialRegistryError(err error) error {
	var typeError *yaml.TypeError
	if errors.As(err, &typeError) {
		for _, problem := range typeError.Errors {
			if strings.Contains(problem, "field ") && strings.Contains(problem, " not found in type ") {
				return fmt.Errorf("registry manifest contains a field this CLI does not recognize; it may require a newer Premise CLI (run `pm update` or rebuild `pm`): %w", err)
			}
		}
	}
	return err
}

func newRegistryEntry(source string, template Template) RegistryEntry {
	return RegistryEntry{Source: source, Template: template, Label: template.Name}
}

// disambiguateLabels appends the source identity to any label whose template
// name is declared by more than one source, so duplicate names stay
// distinguishable in the picker while selectors remain unambiguous.
func disambiguateLabels(entries []RegistryEntry) {
	counts := make(map[string]int, len(entries))
	for _, entry := range entries {
		counts[entry.Template.Name]++
	}
	for index := range entries {
		if counts[entries[index].Template.Name] > 1 {
			entries[index].Label = fmt.Sprintf("%s (%s)", entries[index].Template.Name, entries[index].Source)
		}
	}
}

// resolveRepository resolves a source identity to a local registry checkout.
// It is a package variable so fixture-backed tests can substitute a loader that
// never touches the network or git.
var resolveRepository = resolveRepositoryFromCache

func resolveRepositoryFromCache(ctx context.Context, source string) (string, error) {
	repositoryURL := normalizeRepositoryURL(source)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	cacheRoot := filepath.Join(home, ".premise", "templates")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return "", fmt.Errorf("create template cache: %w", err)
	}
	digest := sha256.Sum256([]byte(repositoryURL))
	cachePath := filepath.Join(cacheRoot, hex.EncodeToString(digest[:16]))

	if _, err := os.Stat(cachePath); err == nil {
		if err := refreshRepository(ctx, cachePath); err != nil {
			return "", err
		}
		return cachePath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect template cache: %w", err)
	}

	tempRoot, err := os.MkdirTemp(cacheRoot, ".clone-*")
	if err != nil {
		return "", fmt.Errorf("create temporary clone directory: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	tempRepository := filepath.Join(tempRoot, "repository")
	if _, err := git.PlainCloneContext(ctx, tempRepository, false, &git.CloneOptions{
		URL:          repositoryURL,
		Depth:        1,
		SingleBranch: true,
	}); err != nil {
		return "", fmt.Errorf("clone template repository: %w", err)
	}
	if err := os.Rename(tempRepository, cachePath); err != nil {
		if _, statErr := os.Stat(cachePath); statErr == nil {
			return cachePath, nil
		}
		return "", fmt.Errorf("install template cache: %w", err)
	}
	return cachePath, nil
}

func normalizeRepositoryURL(source string) string {
	trimmed := strings.TrimSpace(source)
	if !strings.Contains(trimmed, "://") && !strings.Contains(trimmed, "@") && strings.Count(trimmed, "/") == 1 {
		return "https://github.com/" + strings.TrimSuffix(trimmed, ".git") + ".git"
	}
	return trimmed
}

func refreshRepository(ctx context.Context, path string) error {
	repository, err := git.PlainOpen(path)
	if err != nil {
		return fmt.Errorf("open cached template repository: %w", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return fmt.Errorf("open cached template worktree: %w", err)
	}
	if err := worktree.PullContext(ctx, &git.PullOptions{RemoteName: "origin"}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("refresh cached template repository: %w", err)
	}
	return nil
}
