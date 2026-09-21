package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// -----------------------------------------------------------------------------
// Types
// -----------------------------------------------------------------------------

type Questionnaire interface {
	Ask([]Question) (map[string]string, error)
}

type GenerateOptions struct {
	Questionnaire    Questionnaire
	ConflictResolver MergeConflictResolver
}

// GenerateParameters contains roots and choices resolved for one generated project.
type GenerateParameters struct {
	RegistryTemplatesRoot string
	TemplateRoot          string
	ClientRepoRoot        string
	ProjectPath           string
	Substitutions         map[string]string
	TemplateKind          string
	RegistryIdentity      string
	TemplateIdentity      string
	ResolveConflict       MergeConflictResolver
}

type generationSources struct {
	registryTemplatesRoot string
	templateRoot          string
}

type generationStages struct {
	stage         string
	selectedStage string
}

// GeneratePlan is the complete, prepared generation. Its staging paths are
// private so callers can only publish a plan through ApplyGeneratePlan.
type GeneratePlan struct {
	Entries []MergeEntry
	Root    PathValue
	Mise    MiseMergeResult

	destination   string
	stage         string
	selectedStage string
	templateKind  string
}

// -----------------------------------------------------------------------------
// API
// -----------------------------------------------------------------------------

func Generate(ctx context.Context, cwd, selector string, options GenerateOptions, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	if options.Questionnaire == nil {
		return errors.New("generate questionnaire is required")
	}
	workspaceRoot, workspaceManifestPath, err := FindManifest(cwd)
	if err != nil {
		return err
	}
	workspaceManifest, err := LoadManifest(workspaceManifestPath)
	if err != nil {
		return err
	}

	sourceRoot, selection, err := ResolveTemplateSource(ctx, workspaceRoot, selector)
	if err != nil {
		return err
	}
	templateManifestPath := filepath.Join(sourceRoot, ManifestFilename)
	templateManifest, err := LoadManifest(templateManifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("template source %q does not contain %s; choose a Premise template repository or run `pm init` in that directory", sourceRoot, ManifestFilename)
	}
	if err != nil {
		return fmt.Errorf("load template manifest: %w", err)
	}
	template, err := templateManifest.FindTemplate(selection.Name)
	if err != nil {
		return err
	}
	answers, err := options.Questionnaire.Ask(template.Questions)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(answers["name"])
	if err := ValidateProjectName(name); err != nil {
		return fmt.Errorf("project name: %w", err)
	}
	answers["name"] = name

	projectDirectory, err := KindDirectory(template.Kind)
	if err != nil {
		return err
	}
	source, err := TemplateDirectory(sourceRoot, template.Name)
	if err != nil {
		return err
	}
	relativeDestination := filepath.Join(projectDirectory, name)
	destination := filepath.Join(workspaceRoot, relativeDestination)
	replacements, err := ResolveSubstitutions(template, answers)
	if err != nil {
		return err
	}
	plan, err := BuildGeneratePlan(GenerateParameters{
		RegistryTemplatesRoot: filepath.Join(sourceRoot, "templates"),
		TemplateRoot:          source,
		ClientRepoRoot:        workspaceRoot,
		ProjectPath:           relativeDestination,
		Substitutions:         replacements,
		TemplateIdentity:      selector,
		TemplateKind:          template.Kind,
		RegistryIdentity:      selection.Source,
		ResolveConflict:       options.ConflictResolver,
	})
	if err != nil {
		return err
	}
	for _, notice := range plan.Mise.Notices {
		fmt.Fprintf(output, "- %s\n", notice)
	}
	if err := ApplyGeneratePlan(ctx, plan, output); err != nil {
		return err
	}

	project := Project{
		Name:     name,
		Template: selector,
		Version:  template.Version,
		Path:     filepath.ToSlash(relativeDestination),
		Answers:  answers,
	}
	if err := workspaceManifest.AddProject(project); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	if err := SaveManifest(workspaceManifestPath, workspaceManifest); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	fmt.Fprintf(output, "Generated %s %s from %s at %s\n", template.Kind, name, selector, relativeDestination)
	return nil
}

// BuildGeneratePlan validates and prepares a generation without creating the
// project destination. The returned plan owns private staging paths until
// ApplyGeneratePlan.
func BuildGeneratePlan(request GenerateParameters) (*GeneratePlan, error) {
	destination, err := validateGenerationTarget(request)
	if err != nil {
		return nil, err
	}
	sources, err := validateGenerationSources(request, destination)
	if err != nil {
		return nil, err
	}
	stages, err := createGenerationStages(destination)
	if err != nil {
		return nil, err
	}
	plan := &GeneratePlan{
		destination: destination, stage: stages.stage, selectedStage: stages.selectedStage,
		templateKind: request.TemplateKind,
	}
	keepStages := false
	defer func() {
		if !keepStages {
			_ = plan.close()
		}
	}()

	if err := copyTree(sources.templateRoot, plan.selectedStage, literalReplacer(request.Substitutions)); err != nil {
		return nil, fmt.Errorf("stage selected template: %w", err)
	}
	if err := buildGenerationEntries(plan, sources, request); err != nil {
		return nil, err
	}
	keepStages = true
	return plan, nil
}

// ApplyGeneratePlan validates a prepared generation and publishes it with a
// final same-filesystem rename; only that rename is atomic.
func ApplyGeneratePlan(ctx context.Context, plan *GeneratePlan, output io.Writer) (err error) {
	if plan == nil || plan.stage == "" {
		return errors.New("generation plan is nil or already executed")
	}
	if output == nil {
		output = io.Discard
	}
	published := false
	defer func() {
		cleanupErr := plan.close()
		if cleanupErr == nil {
			return
		}
		if published {
			if rollbackErr := os.RemoveAll(plan.destination); rollbackErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("rollback generation destination: %w", rollbackErr))
			}
		}
		err = errors.Join(err, cleanupErr)
	}()

	if err = validateGenerationToolChanges(ctx, plan.selectedStage, plan.templateKind, plan.Mise.ToolChanges, output); err != nil {
		return err
	}
	if err = materializeGeneratePlan(plan, plan.stage); err != nil {
		return err
	}
	if err = validateGeneratedCandidate(ctx, plan.stage, plan.templateKind, output); err != nil {
		return err
	}
	if _, statErr := os.Lstat(plan.destination); statErr == nil {
		return fmt.Errorf("destination %s already exists", plan.destination)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect generation destination: %w", statErr)
	}
	if err = os.Rename(plan.stage, plan.destination); err != nil {
		return fmt.Errorf("commit generation: %w", err)
	}
	plan.stage = ""
	published = true
	return nil
}

// -----------------------------------------------------------------------------
// Utils
// -----------------------------------------------------------------------------

func validateGenerationTarget(request GenerateParameters) (string, error) {
	if request.ClientRepoRoot == "" {
		return "", errors.New("client repository root is required")
	}
	clientRoot, err := filepath.Abs(request.ClientRepoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve client repository root: %w", err)
	}
	if info, err := os.Stat(clientRoot); err != nil {
		return "", fmt.Errorf("inspect client repository root: %w", err)
	} else if !info.IsDir() {
		return "", fmt.Errorf("client repository root %s is not a directory", clientRoot)
	}
	clientRoot, err = filepath.EvalSymlinks(clientRoot)
	if err != nil {
		return "", fmt.Errorf("resolve client repository root: %w", err)
	}
	if request.ProjectPath == "" {
		return "", errors.New("project path is required")
	}
	if filepath.IsAbs(request.ProjectPath) {
		return "", errors.New("project path must be relative")
	}
	destination := filepath.Join(clientRoot, request.ProjectPath)
	if !inside(clientRoot, destination) {
		return "", fmt.Errorf("project path %q escapes client repository root", request.ProjectPath)
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", fmt.Errorf("destination %s already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect generation destination: %w", err)
	}
	resolvedDestination, err := resolveFuturePath(destination)
	if err != nil {
		return "", fmt.Errorf("resolve generation destination: %w", err)
	}
	if !inside(clientRoot, resolvedDestination) {
		return "", fmt.Errorf("project path %q escapes client repository root", request.ProjectPath)
	}
	return resolvedDestination, nil
}

func validateGenerationSources(request GenerateParameters, destination string) (generationSources, error) {
	if request.TemplateRoot == "" {
		return generationSources{}, errors.New("template root is required")
	}
	templateRoot, err := filepath.Abs(request.TemplateRoot)
	if err != nil {
		return generationSources{}, fmt.Errorf("resolve template root: %w", err)
	}
	if info, err := os.Stat(templateRoot); err != nil {
		return generationSources{}, fmt.Errorf("inspect template root: %w", err)
	} else if !info.IsDir() {
		return generationSources{}, fmt.Errorf("template root %s is not a directory", templateRoot)
	}
	templateRoot, err = filepath.EvalSymlinks(templateRoot)
	if err != nil {
		return generationSources{}, fmt.Errorf("resolve template root: %w", err)
	}

	sources := generationSources{templateRoot: templateRoot}
	if request.RegistryTemplatesRoot != "" {
		sources.registryTemplatesRoot, err = filepath.Abs(request.RegistryTemplatesRoot)
		if err != nil {
			return generationSources{}, fmt.Errorf("resolve registry templates root: %w", err)
		}
		if info, err := os.Stat(sources.registryTemplatesRoot); err != nil {
			return generationSources{}, fmt.Errorf("inspect registry templates root: %w", err)
		} else if !info.IsDir() {
			return generationSources{}, fmt.Errorf("registry templates root %s is not a directory", sources.registryTemplatesRoot)
		}
		sources.registryTemplatesRoot, err = filepath.EvalSymlinks(sources.registryTemplatesRoot)
		if err != nil {
			return generationSources{}, fmt.Errorf("resolve registry templates root: %w", err)
		}
	}
	for _, source := range []string{sources.templateRoot, sources.registryTemplatesRoot} {
		if source != "" && inside(source, destination) {
			return generationSources{}, fmt.Errorf("generation destination %s is inside source %s", destination, source)
		}
	}
	return sources, nil
}

func createGenerationStages(destination string) (generationStages, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return generationStages{}, fmt.Errorf("create generation destination parent: %w", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".premise-stage-*")
	if err != nil {
		return generationStages{}, fmt.Errorf("create generation staging directory: %w", err)
	}
	selectedStage, err := os.MkdirTemp("", "premise-selected-*")
	if err != nil {
		_ = os.RemoveAll(stage)
		return generationStages{}, fmt.Errorf("create selected template staging directory: %w", err)
	}
	return generationStages{stage: stage, selectedStage: selectedStage}, nil
}

// resolveFuturePath resolves symlinks in the existing portion of a path while
// preserving the not-yet-created suffix. This keeps a project path inside the
// client repository even when one of its parent directories is a symlink.
func resolveFuturePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var suffix []string
	probe := absolute
	for {
		if _, err := os.Lstat(probe); err == nil {
			resolved, err := filepath.EvalSymlinks(probe)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", errors.New("no existing parent directory")
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
}

func selectPresent(left, right PathValue, leftOK bool) PathValue {
	if leftOK {
		return left
	}
	return right
}

func buildGenerationEntries(plan *GeneratePlan, sources generationSources, request GenerateParameters) error {
	shared, err := readSharedValues(sources.registryTemplatesRoot, literalReplacer(request.Substitutions))
	if err != nil {
		return err
	}
	selected, rootMode, err := readTreeValues(plan.selectedStage, nil)
	if err != nil {
		return err
	}
	plan.Root = PathValue{Present: true, Kind: "dir", Mode: rootMode}
	registryIdentity := request.RegistryIdentity
	if registryIdentity == "" {
		registryIdentity = request.TemplateIdentity
	}
	if registryIdentity == "" {
		registryIdentity = filepath.Base(sources.templateRoot)
	}
	paths := unionPathKeys(shared, selected)
	for _, path := range paths {
		sharedValue, sharedOK := shared[path]
		selectedValue, selectedOK := selected[path]
		sharedValue.Present = sharedOK
		selectedValue.Present = selectedOK
		entry := MergeEntry{Path: path, Shared: sharedValue, Selected: selectedValue}
		switch {
		case !sharedOK || !selectedOK:
			entry.Output = clonePathValue(selectPresent(sharedValue, selectedValue, sharedOK))
			entry.Strategy = MergeStrategyCopy
		case samePathValue(sharedValue, selectedValue):
			entry.Output = clonePathValue(selectedValue)
			entry.Strategy = MergeStrategyEqual
		default:
			conflict := MergeConflict{Path: path, Key: path, Kind: pathKind(sharedValue, selectedValue), Shared: describePathValue(sharedValue), Selected: describePathValue(selectedValue)}
			switch {
			case path == "mise.toml" && sharedValue.Kind == "file" && selectedValue.Kind == "file":
				result, err := mergeTierOneMise(&entry, sharedValue, selectedValue, request.TemplateKind, registryIdentity, request.ResolveConflict)
				if err != nil {
					return err
				}
				plan.Mise = result
			case isOrderedMergePath(path) && sharedValue.Kind == "file" && selectedValue.Kind == "file":
				if err := mergeTierTwoLines(&entry, sharedValue, selectedValue, request.ResolveConflict); err != nil {
					return err
				}
			default:
				if err := mergeTierThreeWholeFile(&entry, conflict, request.ResolveConflict); err != nil {
					return err
				}
			}
		}
		plan.Entries = append(plan.Entries, entry)
	}
	plan.Entries = pruneOutputFileDescendants(plan.Entries)
	return nil
}

func (plan *GeneratePlan) close() error {
	var failures []error
	if plan.stage != "" {
		failures = append(failures, os.RemoveAll(plan.stage))
		plan.stage = ""
	}
	if plan.selectedStage != "" {
		failures = append(failures, os.RemoveAll(plan.selectedStage))
		plan.selectedStage = ""
	}
	return errors.Join(failures...)
}

func validateGenerationToolChanges(ctx context.Context, selectedStage, kind string, changes []ToolChange, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	for _, change := range changes {
		fmt.Fprintf(output, "Validating tool %s %v -> %v...\n", change.Name, change.From, change.To)
		candidate, err := os.MkdirTemp("", "premise-tool-preflight-*")
		if err != nil {
			return fmt.Errorf("create %s tool preflight: %w", change.Name, err)
		}
		failure := func() error {
			defer os.RemoveAll(candidate)
			if err := copyTree(selectedStage, candidate, nil); err != nil {
				return err
			}
			if err := updateMiseTool(filepath.Join(candidate, "mise.toml"), change.Name, change.To); err != nil {
				return err
			}
			label := fmt.Sprintf("tool %s %v -> %v", change.Name, change.From, change.To)
			if err := runGenerationTemplateContracts(ctx, candidate, kind, label, output); err != nil {
				return fmt.Errorf("tool update %s %v -> %v failed: %w", change.Name, change.From, change.To, err)
			}
			return nil
		}()
		if failure != nil {
			return failure
		}
	}
	return nil
}

func validateGeneratedCandidate(ctx context.Context, stage, kind string, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	fmt.Fprintln(output, "Validating generated candidate...")
	candidate, err := os.MkdirTemp("", "premise-candidate-validation-*")
	if err != nil {
		return fmt.Errorf("create generated candidate validation copy: %w", err)
	}
	defer os.RemoveAll(candidate)
	if err := copyTree(stage, candidate, nil); err != nil {
		return fmt.Errorf("copy generated candidate: %w", err)
	}
	if err := runGenerationTemplateContracts(ctx, candidate, kind, "generated candidate", output); err != nil {
		return fmt.Errorf("generated candidate validation failed: %w", err)
	}
	return nil
}

func materializeGeneratePlan(plan *GeneratePlan, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("create generation stage: %w", err)
	}
	var directories []MergeEntry
	for _, entry := range plan.Entries {
		target := filepath.Join(destination, filepath.FromSlash(entry.Path))
		if !inside(destination, target) {
			return fmt.Errorf("merge entry escapes generation root: %s", target)
		}
		switch entry.Output.Kind {
		case "dir":
			if err := os.MkdirAll(target, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("create generation directory %s: %w", entry.Path, err)
			}
			directories = append(directories, entry)
		case "file":
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create generation parent %s: %w", entry.Path, err)
			}
			if err := os.WriteFile(target, entry.Output.Data, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("write generated file %s: %w", entry.Path, err)
			}
			if err := os.Chmod(target, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("preserve generated file permissions %s: %w", entry.Path, err)
			}
		default:
			return fmt.Errorf("unsupported output kind %q for %s", entry.Output.Kind, entry.Path)
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.Count(directories[i].Path, "/") > strings.Count(directories[j].Path, "/")
	})
	for _, entry := range directories {
		if err := os.Chmod(filepath.Join(destination, filepath.FromSlash(entry.Path)), entry.Output.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve generated directory permissions %s: %w", entry.Path, err)
		}
	}
	if plan.Root.Present {
		if err := os.Chmod(destination, plan.Root.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve generation root permissions: %w", err)
		}
	}
	return nil
}

func readSharedValues(root string, replacer *strings.Replacer) (map[string]PathValue, error) {
	values := make(map[string]PathValue)
	if root == "" {
		return values, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read shared registry root: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect shared entry %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("unsupported shared entry %s", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read shared file %s: %w", entry.Name(), err)
		}
		if replacer != nil && isText(data) {
			data = []byte(replacer.Replace(string(data)))
		}
		values[filepath.ToSlash(entry.Name())] = PathValue{Kind: "file", Mode: info.Mode().Perm(), Data: data}
	}
	return values, nil
}

func readTreeValues(root string, replacer *strings.Replacer) (map[string]PathValue, fs.FileMode, error) {
	values := make(map[string]PathValue)
	var rootMode fs.FileMode
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || escapesRoot(relative) {
			return fmt.Errorf("source entry escapes generation root: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect generation entry %s: %w", path, err)
		}
		if relative == "." {
			if !info.IsDir() {
				return fmt.Errorf("generation root %s is not a directory", path)
			}
			rootMode = info.Mode().Perm()
			return nil
		}
		key := filepath.ToSlash(relative)
		if info.IsDir() {
			values[key] = PathValue{Kind: "dir", Mode: info.Mode().Perm()}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported merge entry %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read generation file %s: %w", relative, err)
		}
		if replacer != nil && isText(data) {
			data = []byte(replacer.Replace(string(data)))
		}
		values[key] = PathValue{Kind: "file", Mode: info.Mode().Perm(), Data: data}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("read template tree: %w", err)
	}
	return values, rootMode, nil
}

func unionPathKeys(left, right map[string]PathValue) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for path := range left {
		seen[path] = struct{}{}
	}
	for path := range right {
		seen[path] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func pruneOutputFileDescendants(entries []MergeEntry) []MergeEntry {
	output := make([]MergeEntry, 0, len(entries))
	for _, entry := range entries {
		prune := false
		for _, ancestor := range output {
			if ancestor.Output.Kind == "file" && strings.HasPrefix(entry.Path, ancestor.Path+"/") {
				prune = true
				break
			}
		}
		if !prune {
			output = append(output, entry)
		}
	}
	return output
}
