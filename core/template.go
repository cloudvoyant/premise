package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const NativeTemplateSource = "cloudvoyant/premise"

func ResolveTemplateSource(ctx context.Context, workspaceRoot, selector string) (string, TemplateSelection, error) {
	selection, err := ParseTemplateSelector(selector)
	if err != nil {
		return "", TemplateSelection{}, err
	}
	if selection.Local {
		if filepath.IsAbs(selection.Source) {
			return filepath.Clean(selection.Source), selection, nil
		}
		path, err := filepath.Abs(filepath.Join(workspaceRoot, selection.Source))
		if err != nil {
			return "", TemplateSelection{}, fmt.Errorf("resolve local template source: %w", err)
		}
		return path, selection, nil
	}

	root, err := resolveRepository(ctx, selection.Source)
	if err != nil {
		return "", TemplateSelection{}, fmt.Errorf("resolve template registry %s: %w", selection.Source, err)
	}
	return root, selection, nil
}

func TemplateDirectory(sourceRoot, name string) (string, error) {
	if err := ValidateTemplateName(name); err != nil {
		return "", fmt.Errorf("validate template directory name: %w", err)
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve template root: %w", err)
	}
	path := filepath.Join(root, "templates", name)
	relative, err := filepath.Rel(root, path)
	if err != nil || escapesRoot(relative) {
		return "", fmt.Errorf("template path escapes source root")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect template directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("template path %s is not a directory", path)
	}
	return path, nil
}

type ScaffoldRequest struct {
	SharedSource     string
	Source           string
	Destination      string
	Replacements     map[string]string
	SelectedIdentity string
	Resolver         MergeResolver
}

type PreparedScaffold struct {
	Destination   string
	Stage         string
	SelectedStage string
	Plan          TemplateMergePlan
	Mise          MiseMergeResult
}

func PrepareScaffold(request ScaffoldRequest) (*PreparedScaffold, error) {
	source, err := filepath.Abs(request.Source)
	if err != nil {
		return nil, fmt.Errorf("resolve scaffold source: %w", err)
	}
	destination, err := filepath.Abs(request.Destination)
	if err != nil {
		return nil, fmt.Errorf("resolve scaffold destination: %w", err)
	}
	if source == destination {
		return nil, errors.New("scaffold source and destination must differ")
	}
	if info, err := os.Stat(source); err != nil {
		return nil, fmt.Errorf("inspect scaffold source: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("scaffold source %s is not a directory", source)
	}

	sharedSource := ""
	if request.SharedSource != "" {
		sharedSource, err = filepath.Abs(request.SharedSource)
		if err != nil {
			return nil, fmt.Errorf("resolve shared scaffold source: %w", err)
		}
		if info, err := os.Stat(sharedSource); err != nil {
			return nil, fmt.Errorf("inspect shared scaffold source: %w", err)
		} else if !info.IsDir() {
			return nil, fmt.Errorf("shared scaffold source %s is not a directory", sharedSource)
		}
	}
	if _, err := os.Lstat(destination); err == nil {
		return nil, fmt.Errorf("destination %s already exists", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect scaffold destination: %w", err)
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create destination parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".premise-stage-*")
	if err != nil {
		return nil, fmt.Errorf("create scaffold staging directory: %w", err)
	}
	selectedStage, err := os.MkdirTemp("", "premise-selected-*")
	if err != nil {
		_ = os.RemoveAll(stage)
		return nil, fmt.Errorf("create selected template staging directory: %w", err)
	}
	prepared := &PreparedScaffold{Destination: destination, Stage: stage, SelectedStage: selectedStage}
	clean := true
	defer func() {
		if clean {
			_ = prepared.Close()
		}
	}()
	if err := copyTree(source, selectedStage, literalReplacer(request.Replacements)); err != nil {
		return nil, fmt.Errorf("stage selected scaffold: %w", err)
	}
	identity := request.SelectedIdentity
	if identity == "" {
		identity = filepath.Base(source)
	}
	plan, err := BuildTemplateMergePlan(sharedSource, source, request.Replacements, identity, request.Resolver)
	if err != nil {
		return nil, err
	}
	prepared.Plan = plan
	prepared.Mise = plan.Mise
	clean = false
	return prepared, nil
}

func (prepared *PreparedScaffold) Materialize() error {
	if prepared.Stage == "" {
		return errors.New("prepared scaffold is already committed or closed")
	}
	return MaterializeTemplateMerge(prepared.Plan, prepared.Stage)
}

func (prepared *PreparedScaffold) Commit() error {
	if prepared.Stage == "" {
		return errors.New("prepared scaffold is already committed or closed")
	}
	if _, err := os.Lstat(prepared.Destination); err == nil {
		return fmt.Errorf("destination %s already exists", prepared.Destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect scaffold destination: %w", err)
	}
	if err := os.Rename(prepared.Stage, prepared.Destination); err != nil {
		return fmt.Errorf("commit scaffold: %w", err)
	}
	prepared.Stage = ""
	return nil
}

func (prepared *PreparedScaffold) Close() error {
	var failures []error
	if prepared.Stage != "" {
		failures = append(failures, os.RemoveAll(prepared.Stage))
		prepared.Stage = ""
	}
	if prepared.SelectedStage != "" {
		failures = append(failures, os.RemoveAll(prepared.SelectedStage))
		prepared.SelectedStage = ""
	}
	return errors.Join(failures...)
}

func Scaffold(request ScaffoldRequest) error {
	prepared, err := PrepareScaffold(request)
	if err != nil {
		return err
	}
	defer prepared.Close()
	if err := prepared.Materialize(); err != nil {
		return err
	}
	return prepared.Commit()
}

func RenderTemplateMise(kind string) (string, error) {
	tasks, err := ContractTasks(kind)
	if err != nil {
		return "", fmt.Errorf("resolve %s template tasks: %w", kind, err)
	}
	name := "premise-" + kind
	var content strings.Builder
	content.WriteString("# Generated by premise. Replace echo commands with real implementations.\n\n")
	for _, task := range tasks {
		fmt.Fprintf(&content, "[tasks.%q]\n", task)
		fmt.Fprintf(&content, "description = %q\n", "Run the "+task+" contract")
		fmt.Fprintf(&content, "run = %q\n\n", "echo \""+name+": "+task+"\"")
	}
	return content.String(), nil
}

func InitializeTemplate(root, kind string) (string, error) {
	monorepo, err := hasMonorepoRoot(root)
	if err != nil {
		return "", fmt.Errorf("detect project kind: %w", err)
	}
	if monorepo {
		return "", errors.New("cannot add registry templates to a monorepo project")
	}
	manifestPath := filepath.Join(root, ManifestFilename)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return "", fmt.Errorf("load template manifest: %w", err)
	}
	if _, err := manifest.FindTemplate(kind); err == nil {
		return "", fmt.Errorf("template %q is already declared", kind)
	}
	if _, err := KindDirectory(kind); err != nil {
		return "", fmt.Errorf("validate template kind: %w", err)
	}

	templatePath := filepath.Join(root, "templates", kind)
	if _, err := os.Lstat(templatePath); err == nil {
		return "", fmt.Errorf("template directory %s already exists", templatePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect template directory: %w", err)
	}
	if err := os.MkdirAll(templatePath, 0o755); err != nil {
		return "", fmt.Errorf("create template directory: %w", err)
	}
	created := true
	defer func() {
		if created {
			_ = os.RemoveAll(templatePath)
		}
	}()

	mise, err := RenderTemplateMise(kind)
	if err != nil {
		return "", fmt.Errorf("render template Mise config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(templatePath, "mise.toml"), []byte(mise), 0o644); err != nil {
		return "", fmt.Errorf("write template mise.toml: %w", err)
	}
	manifest.Templates = append(manifest.Templates, Template{
		Name:    kind,
		Kind:    kind,
		Version: "0.1.0",
		Questions: []Question{{
			Prompt:   kindLabel(kind) + " name:",
			Type:     "string",
			Populate: "name",
		}},
		Substitutions: map[string]string{"premise-" + kind: "name"},
	})
	if err := SaveManifest(manifestPath, manifest); err != nil {
		return "", fmt.Errorf("save template manifest: %w", err)
	}
	created = false
	return templatePath, nil
}

func TestTemplateContracts(ctx context.Context, root string, manifest Config, stdout, stderr io.Writer) error {
	if len(manifest.Templates) == 0 {
		return errors.New("manifest declares no templates")
	}
	var failures []error
	for _, template := range manifest.Templates {
		directory, err := TemplateDirectory(root, template.Name)
		if err != nil {
			failures = append(failures, fmt.Errorf("template %s: %w", template.Name, err))
			continue
		}
		if err := runTemplateContracts(ctx, directory, template.Kind, template.Name, stdout, stderr); err != nil {
			failures = append(failures, err)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("template contract failures: %w", err)
	}
	return nil
}

func runTemplateContracts(ctx context.Context, directory, kind, label string, stdout, stderr io.Writer) error {
	tasks, err := ContractTasks(kind)
	if err != nil {
		return fmt.Errorf("template %s: %w", label, err)
	}
	mise := miseRunner{Stdout: stdout, Stderr: stderr, Ceiling: filepath.Dir(filepath.Clean(directory))}
	testEnvironment := []string{"PREMISE_TEMPLATE_TEST=1"}
	var failures []error
	fmt.Fprintf(stdout, "[%s] mise install\n", label)
	if err := mise.run(ctx, directory, testEnvironment, "install"); err != nil {
		failures = append(failures, fmt.Errorf("template %s tool install failed: %w", label, err))
	}
	for _, task := range tasks {
		fmt.Fprintf(stdout, "[%s] mise run %s\n", label, task)
		if err := mise.run(ctx, directory, testEnvironment, "run", task); err != nil {
			failures = append(failures, fmt.Errorf("template %s task %s failed: %w", label, task, err))
		}
	}
	return errors.Join(failures...)
}

func copyTree(source, destination string, replacer *strings.Replacer) error {
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || escapesRoot(relative) {
			return fmt.Errorf("source entry escapes scaffold root: %s", path)
		}
		target := destination
		if relative != "." {
			target = filepath.Join(destination, relative)
		}
		if !inside(destination, target) {
			return fmt.Errorf("destination entry escapes scaffold root: %s", target)
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect scaffold entry %s: %w", path, err)
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("create scaffold directory %s: %w", relative, err)
			}
			if err := os.Chmod(target, info.Mode().Perm()); err != nil {
				return fmt.Errorf("preserve directory permissions %s: %w", relative, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported scaffold entry %s", relative)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read scaffold file %s: %w", relative, err)
		}
		if replacer != nil && isText(data) {
			data = []byte(replacer.Replace(string(data)))
		}
		if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
			return fmt.Errorf("write scaffold file %s: %w", relative, err)
		}
		if err := os.Chmod(target, info.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve file permissions %s: %w", relative, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("copy template tree: %w", err)
	}
	return nil
}

func literalReplacer(replacements map[string]string) *strings.Replacer {
	if len(replacements) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(replacements))
	for token := range replacements {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool {
		if len(tokens[i]) == len(tokens[j]) {
			return tokens[i] < tokens[j]
		}
		return len(tokens[i]) > len(tokens[j])
	})
	pairs := make([]string, 0, len(tokens)*2)
	for _, token := range tokens {
		pairs = append(pairs, token, replacements[token])
	}
	return strings.NewReplacer(pairs...)
}

func isText(data []byte) bool {
	return !bytes.ContainsRune(data, 0) && utf8.Valid(data)
}

func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && !escapesRoot(relative)
}

func kindLabel(kind string) string {
	if kind == "app" {
		return "App"
	}
	return "Library"
}
