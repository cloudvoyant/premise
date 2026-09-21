package core

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// Entries contains registry-root files resolved against the client workspace
	// root. The selected template is staged separately for its apps/ or libs/
	// destination and never participates in these root-file conflicts.
	Entries []MergeEntry
	Root    PathValue
	Mise    MiseMergeResult

	workspaceRoot string
	projectPath   string
	destination   string
	stage         string
	selectedStage string
	templateKind  string
	rootBackups   []MergeEntry
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
		return errors.Join(err, removeGeneratedDestination(destination), plan.rollbackRoot())
	}
	if err := SaveManifest(workspaceManifestPath, workspaceManifest); err != nil {
		return errors.Join(err, removeGeneratedDestination(destination), plan.rollbackRoot())
	}
	plan.rootBackups = nil
	fmt.Fprintf(output, "Generated %s %s from %s at %s\n", template.Kind, name, selector, relativeDestination)
	return nil
}

// BuildGeneratePlan validates and prepares a generation without creating the
// project destination. The returned plan owns private staging paths until
// ApplyGeneratePlan.
func BuildGeneratePlan(request GenerateParameters) (*GeneratePlan, error) {
	workspaceRoot, destination, err := validateGenerationTarget(request)
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
		workspaceRoot: workspaceRoot,
		projectPath:   filepath.Clean(request.ProjectPath),
		destination:   destination,
		stage:         stages.stage,
		selectedStage: stages.selectedStage,
		templateKind:  request.TemplateKind,
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
			cleanupErr = errors.Join(cleanupErr, plan.rollbackRoot())
		}
		err = errors.Join(err, cleanupErr)
	}()

	if err = materializeGeneratePlan(plan, plan.stage); err != nil {
		return err
	}
	if err = validateGeneratePlanToolChanges(ctx, plan, output); err != nil {
		return err
	}
	if err = validateGeneratePlanCandidate(ctx, plan, output); err != nil {
		return err
	}
	if _, statErr := os.Lstat(plan.destination); statErr == nil {
		return fmt.Errorf("destination %s already exists", plan.destination)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect generation destination: %w", statErr)
	}
	if err = plan.publishRoot(); err != nil {
		return err
	}
	if err = os.Rename(plan.stage, plan.destination); err != nil {
		return errors.Join(fmt.Errorf("commit generation: %w", err), plan.rollbackRoot())
	}
	plan.stage = ""
	published = true
	return nil
}

// -----------------------------------------------------------------------------
// Utils
// -----------------------------------------------------------------------------

func validateGenerationTarget(request GenerateParameters) (string, string, error) {
	if request.ClientRepoRoot == "" {
		return "", "", errors.New("client repository root is required")
	}
	clientRoot, err := filepath.Abs(request.ClientRepoRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve client repository root: %w", err)
	}
	if info, err := os.Stat(clientRoot); err != nil {
		return "", "", fmt.Errorf("inspect client repository root: %w", err)
	} else if !info.IsDir() {
		return "", "", fmt.Errorf("client repository root %s is not a directory", clientRoot)
	}
	clientRoot, err = filepath.EvalSymlinks(clientRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve client repository root: %w", err)
	}
	if request.ProjectPath == "" {
		return "", "", errors.New("project path is required")
	}
	if filepath.IsAbs(request.ProjectPath) {
		return "", "", errors.New("project path must be relative")
	}
	destination := filepath.Join(clientRoot, request.ProjectPath)
	if !inside(clientRoot, destination) {
		return "", "", fmt.Errorf("project path %q escapes client repository root", request.ProjectPath)
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", "", fmt.Errorf("destination %s already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("inspect generation destination: %w", err)
	}
	resolvedDestination, err := resolveFuturePath(destination)
	if err != nil {
		return "", "", fmt.Errorf("resolve generation destination: %w", err)
	}
	if !inside(clientRoot, resolvedDestination) {
		return "", "", fmt.Errorf("project path %q escapes client repository root", request.ProjectPath)
	}
	return clientRoot, resolvedDestination, nil
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

func buildGenerationEntries(plan *GeneratePlan, sources generationSources, request GenerateParameters) error {
	shared, err := readSharedValues(sources.registryTemplatesRoot, literalReplacer(request.Substitutions))
	if err != nil {
		return err
	}
	selectedInfo, err := os.Stat(plan.selectedStage)
	if err != nil {
		return fmt.Errorf("inspect selected template stage: %w", err)
	}
	plan.Root = PathValue{Present: true, Kind: "dir", Mode: selectedInfo.Mode().Perm()}
	paths := unionPathKeys(shared, nil)
	client, err := readClientRootValues(plan.workspaceRoot, paths)
	if err != nil {
		return err
	}
	registryIdentity := request.RegistryIdentity
	if registryIdentity == "" {
		registryIdentity = request.TemplateIdentity
	}
	if registryIdentity == "" {
		registryIdentity = filepath.Base(sources.templateRoot)
	}
	for _, path := range paths {
		sharedValue := shared[path]
		clientValue, clientOK := client[path]
		sharedValue.Present = true
		clientValue.Present = clientOK
		entry := MergeEntry{Path: path, Shared: sharedValue, Selected: clientValue}
		switch {
		case !clientOK:
			entry.Output = clonePathValue(sharedValue)
			entry.Strategy = MergeStrategyCopy
		case samePathValue(sharedValue, clientValue):
			entry.Output = clonePathValue(clientValue)
			entry.Strategy = MergeStrategyEqual
		default:
			conflict := MergeConflict{Path: path, Key: path, Kind: pathKind(sharedValue, clientValue), Shared: describePathValue(sharedValue), Selected: describePathValue(clientValue)}
			switch {
			case path == "mise.toml" && sharedValue.Kind == "file" && clientValue.Kind == "file":
				result, err := mergeTierOneMise(&entry, sharedValue, clientValue, request.TemplateKind, registryIdentity, request.ResolveConflict)
				if err != nil {
					return err
				}
				plan.Mise = result
			case isOrderedMergePath(path) && sharedValue.Kind == "file" && clientValue.Kind == "file":
				if err := mergeTierTwoLines(&entry, sharedValue, clientValue, request.ResolveConflict); err != nil {
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

func validateGeneratePlanToolChanges(ctx context.Context, plan *GeneratePlan, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	for _, change := range plan.Mise.ToolChanges {
		fmt.Fprintf(output, "Validating tool %s %v -> %v...\n", change.Name, change.From, change.To)
		candidate, err := os.MkdirTemp("", "premise-tool-preflight-*")
		if err != nil {
			return fmt.Errorf("create %s tool preflight: %w", change.Name, err)
		}
		failure := func() error {
			defer os.RemoveAll(candidate)
			if err := prepareWorkspaceCandidate(plan, candidate, false); err != nil {
				return err
			}
			if err := updateMiseTool(filepath.Join(candidate, "mise.toml"), change.Name, change.To); err != nil {
				return err
			}
			label := fmt.Sprintf("tool %s %v -> %v", change.Name, change.From, change.To)
			if err := validateWorkspaceCandidateContracts(ctx, candidate, plan.projectPath, plan.templateKind, label, output); err != nil {
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

func validateGeneratePlanCandidate(ctx context.Context, plan *GeneratePlan, output io.Writer) error {
	if output == nil {
		output = io.Discard
	}
	fmt.Fprintln(output, "Validating generated candidate...")
	candidate, err := os.MkdirTemp("", "premise-candidate-validation-*")
	if err != nil {
		return fmt.Errorf("create generated candidate validation copy: %w", err)
	}
	defer os.RemoveAll(candidate)
	if err := prepareWorkspaceCandidate(plan, candidate, true); err != nil {
		return fmt.Errorf("prepare generated workspace candidate: %w", err)
	}
	if err := validateWorkspaceCandidateContracts(ctx, candidate, plan.projectPath, plan.templateKind, "generated candidate", output); err != nil {
		return fmt.Errorf("generated candidate validation failed: %w", err)
	}
	return nil
}

func prepareWorkspaceCandidate(plan *GeneratePlan, candidate string, mergedRoot bool) error {
	if err := copyClientRootFiles(plan.workspaceRoot, candidate); err != nil {
		return err
	}
	if err := materializeRootEntries(plan.Entries, candidate, !mergedRoot); err != nil {
		return err
	}
	project := filepath.Join(candidate, plan.projectPath)
	if !inside(candidate, project) {
		return fmt.Errorf("candidate project path escapes workspace: %s", plan.projectPath)
	}
	return copyTree(plan.stage, project, nil)
}

func validateWorkspaceCandidateContracts(ctx context.Context, candidate, projectPath, kind, label string, output io.Writer) error {
	rootMise := filepath.Join(candidate, "mise.toml")
	if data, err := os.ReadFile(rootMise); err == nil {
		config, err := ExtractMiseConfig("generated workspace", data)
		if err != nil {
			return err
		}
		tasks, err := ContractTasks(kind)
		if err != nil {
			return err
		}
		complete := true
		for _, task := range tasks {
			if _, ok := config.Tasks[task]; !ok {
				complete = false
				break
			}
		}
		if complete {
			if err := runGenerationTemplateContracts(ctx, candidate, kind, label+" workspace", output); err != nil {
				return err
			}
		} else if err := runGenerationMiseInstall(ctx, candidate, label+" workspace", output); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	project := filepath.Join(candidate, projectPath)
	return runGenerationTemplateContractsWithCeiling(ctx, project, kind, label+" project", candidate, output)
}

func materializeGeneratePlan(plan *GeneratePlan, destination string) error {
	if err := copyTree(plan.selectedStage, destination, nil); err != nil {
		return fmt.Errorf("materialize selected template: %w", err)
	}
	if plan.Root.Present {
		if err := os.Chmod(destination, plan.Root.Mode.Perm()); err != nil {
			return fmt.Errorf("preserve generation root permissions: %w", err)
		}
	}
	return nil
}

func materializeRootEntries(entries []MergeEntry, root string, skipMise bool) error {
	for _, entry := range entries {
		if skipMise && entry.Path == "mise.toml" {
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(entry.Path))
		if !inside(root, target) {
			return fmt.Errorf("root merge entry escapes workspace: %s", target)
		}
		switch entry.Output.Kind {
		case "dir":
			if err := os.MkdirAll(target, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("create root directory %s: %w", entry.Path, err)
			}
		case "file":
			if info, err := os.Lstat(target); err == nil && info.IsDir() {
				return fmt.Errorf("cannot replace workspace directory %s with a shared file", entry.Path)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.WriteFile(target, entry.Output.Data, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("write workspace root file %s: %w", entry.Path, err)
			}
			if err := os.Chmod(target, entry.Output.Mode.Perm()); err != nil {
				return fmt.Errorf("preserve workspace root file permissions %s: %w", entry.Path, err)
			}
		default:
			return fmt.Errorf("unsupported root output kind %q for %s", entry.Output.Kind, entry.Path)
		}
	}
	return nil
}

func readClientRootValues(root string, names []string) (map[string]PathValue, error) {
	values := make(map[string]PathValue, len(names))
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if !inside(root, path) {
			return nil, fmt.Errorf("client root entry escapes workspace: %s", name)
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect client root entry %s: %w", name, err)
		}
		value := PathValue{Present: true, Mode: info.Mode().Perm()}
		switch {
		case info.IsDir():
			value.Kind = "dir"
		case info.Mode().IsRegular():
			value.Kind = "file"
			value.Data, err = os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read client root file %s: %w", name, err)
			}
		default:
			return nil, fmt.Errorf("unsupported client root entry %s", name)
		}
		values[name] = value
	}
	return values, nil
}

func copyClientRootFiles(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read client workspace root: %w", err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect client workspace file %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return fmt.Errorf("read client workspace file %s: %w", entry.Name(), err)
		}
		target := filepath.Join(destination, entry.Name())
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return fmt.Errorf("copy client workspace file %s: %w", entry.Name(), err)
		}
		if err := os.Chmod(target, info.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve client workspace file permissions %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (plan *GeneratePlan) publishRoot() error {
	plan.rootBackups = nil
	for _, entry := range plan.Entries {
		if entry.Selected.Present && samePathValue(entry.Output, entry.Selected) {
			continue
		}
		if entry.Output.Kind != "file" {
			return errors.Join(fmt.Errorf("cannot publish shared root %s as %s", entry.Path, entry.Output.Kind), plan.rollbackRoot())
		}
		target := filepath.Join(plan.workspaceRoot, filepath.FromSlash(entry.Path))
		if info, err := os.Lstat(target); err == nil && info.IsDir() {
			return errors.Join(fmt.Errorf("cannot replace workspace directory %s with a shared file", entry.Path), plan.rollbackRoot())
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Join(err, plan.rollbackRoot())
		}
		backup := entry.Selected
		backup.Data = append([]byte(nil), backup.Data...)
		plan.rootBackups = append(plan.rootBackups, MergeEntry{Path: entry.Path, Selected: backup})
		if err := os.WriteFile(target, entry.Output.Data, entry.Output.Mode.Perm()); err != nil {
			return errors.Join(fmt.Errorf("write workspace root file %s: %w", entry.Path, err), plan.rollbackRoot())
		}
		if err := os.Chmod(target, entry.Output.Mode.Perm()); err != nil {
			return errors.Join(fmt.Errorf("preserve workspace root file permissions %s: %w", entry.Path, err), plan.rollbackRoot())
		}
	}
	return nil
}

func removeGeneratedDestination(destination string) error {
	if err := os.RemoveAll(destination); err != nil {
		return fmt.Errorf("rollback generation destination: %w", err)
	}
	return nil
}

func (plan *GeneratePlan) rollbackRoot() error {
	var failures []error
	for index := len(plan.rootBackups) - 1; index >= 0; index-- {
		backup := plan.rootBackups[index]
		target := filepath.Join(plan.workspaceRoot, filepath.FromSlash(backup.Path))
		if !backup.Selected.Present {
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("remove published workspace root file %s: %w", backup.Path, err))
			}
			continue
		}
		if backup.Selected.Kind != "file" {
			failures = append(failures, fmt.Errorf("cannot restore workspace root %s as %s", backup.Path, backup.Selected.Kind))
			continue
		}
		if err := os.WriteFile(target, backup.Selected.Data, backup.Selected.Mode.Perm()); err != nil {
			failures = append(failures, fmt.Errorf("restore workspace root file %s: %w", backup.Path, err))
			continue
		}
		if err := os.Chmod(target, backup.Selected.Mode.Perm()); err != nil {
			failures = append(failures, fmt.Errorf("restore workspace root file permissions %s: %w", backup.Path, err))
		}
	}
	plan.rootBackups = nil
	return errors.Join(failures...)
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
