package core

// CI module responsibilities:
//   - select root overrides or project-kind lifecycle fallbacks;
//   - preserve task ordering and per-template failure isolation;
//   - gate release phases without implementing release or tool policy.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CIFlow names one convention-driven CI lifecycle.
type CIFlow string

const (
	CIFlowOnCommit   CIFlow = "on-commit"
	CIFlowOnMerge    CIFlow = "on-merge"
	CIFlowOnRelease  CIFlow = "on-release"
	CIFlowOnPlatform CIFlow = "on-platform"
)

// CIPlatformProject is the shared, build-system-neutral project selection.
type CIPlatformProject struct {
	Name             string   `json:"name"`
	Path             string   `json:"path"`
	Kind             string   `json:"kind"`
	Source           string   `json:"source"`
	CheckPlatforms   []string `json:"check_platforms"`
	ReleasePlatforms []string `json:"release_platforms"`
	Eligible         bool     `json:"eligible"`
	ExclusionReasons []string `json:"exclusion_reasons"`
}

// HostCIPlatform returns the logical platform for the current host.
func HostCIPlatform() (string, error) {
	switch runtime.GOOS {
	case "linux":
		return "linux", nil
	case "darwin":
		return "macos", nil
	case "windows":
		return "windows", nil
	default:
		return "", fmt.Errorf("unsupported CI host %q", runtime.GOOS)
	}
}

// CIRunner maps a logical platform to its fixed GitHub runner image.
func CIRunner(platform string) (string, error) {
	switch platform {
	case "linux":
		return "ubuntu-22.04", nil
	case "macos":
		return "macos-14", nil
	case "windows":
		return "windows-2022", nil
	default:
		return "", fmt.Errorf("unknown CI platform %q", platform)
	}
}

func (project CIPlatformProject) Platforms() []string {
	platforms := append([]string{}, project.CheckPlatforms...)
	for _, release := range project.ReleasePlatforms {
		if !slices.Contains(platforms, release) {
			platforms = append(platforms, release)
		}
	}
	return platforms
}

// SelectCIProjects normalizes declared projects in manifest order and checks paths.
func SelectCIProjects(root string, manifest Config, flow CIFlow) ([]CIPlatformProject, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if flow == CIFlowOnPlatform {
		return nil, errors.New("on-platform is not a project selection flow")
	}
	host, err := HostCIPlatform()
	if err != nil {
		return nil, err
	}
	out := make([]CIPlatformProject, 0)
	if manifest.Workspace.Kind == ProjectKindTemplateRegistry {
		for _, template := range manifest.DeclaredTemplates() {
			if _, err := TemplateDirectory(root, template.Path); err != nil {
				return nil, fmt.Errorf("template %q: %w", template.Name, err)
			}
			out = append(out, ciProject(template.Name, template.Path, template.Kind, "registry", template.CI, host))
		}
	} else {
		for _, project := range manifest.Workspace.Projects {
			if _, err := TemplateDirectory(root, project.Path); err != nil {
				return nil, fmt.Errorf("project %q: %w", project.Name, err)
			}
			kind := ""
			switch {
			case strings.HasPrefix(project.Path, "apps/"):
				kind = "app"
			case strings.HasPrefix(project.Path, "libs/"):
				kind = "lib"
			default:
				return nil, fmt.Errorf("project %q must be inside apps/ or libs/", project.Name)
			}
			out = append(out, ciProject(project.Name, project.Path, kind, "workspace", project.CI, host))
		}
	}
	seenNames := map[string]struct{}{}
	seenPaths := map[string]struct{}{}
	for _, project := range out {
		if _, exists := seenNames[project.Name]; exists {
			return nil, fmt.Errorf("duplicate project identity %q", project.Name)
		}
		if _, exists := seenPaths[project.Path]; exists {
			return nil, fmt.Errorf("duplicate project path %q", project.Path)
		}
		seenNames[project.Name] = struct{}{}
		seenPaths[project.Path] = struct{}{}
	}
	return out, nil
}

func ciProject(name, path, kind, source string, declaration PlatformDeclaration, host string) CIPlatformProject {
	checks := append([]string{}, declaration.CheckPlatforms...)
	if len(checks) == 0 {
		checks = []string{host}
	}
	project := CIPlatformProject{
		Name: name, Path: path, Kind: kind, Source: source,
		CheckPlatforms:   checks,
		ReleasePlatforms: append([]string{}, declaration.ReleasePlatforms...),
		ExclusionReasons: []string{},
	}
	platforms := project.Platforms()
	switch {
	case len(platforms) > 1:
		project.ExclusionReasons = append(project.ExclusionReasons, "multiple platforms: use --project and --platform")
	case len(platforms) == 1 && platforms[0] != host:
		project.ExclusionReasons = append(project.ExclusionReasons, "requires "+platforms[0]+" host")
	}
	project.Eligible = len(project.ExclusionReasons) == 0
	return project
}

// CIMatrixRow describes one complete project/platform flow, not an internal phase.
type CIMatrixRow struct {
	Project  string `json:"project"`
	Platform string `json:"platform"`
	Runner   string `json:"runner"`
	Checks   bool   `json:"checks"`
	Native   bool   `json:"native"`
	Artifact string `json:"artifact"`
}

type CISchedule struct {
	Mode     string              `json:"mode"`
	Runner   string              `json:"runner,omitempty"`
	Matrix   []CIMatrixRow       `json:"matrix"`
	Excluded []CIPlatformProject `json:"excluded"`
}

// PlanCISchedule chooses one whole-runner flow or one unified matrix.
func PlanCISchedule(projects []CIPlatformProject) (CISchedule, error) {
	plan := CISchedule{Matrix: []CIMatrixRow{}, Excluded: []CIPlatformProject{}}
	platforms := make(map[string]bool)
	for _, project := range projects {
		if !project.Eligible {
			plan.Excluded = append(plan.Excluded, project)
		}
		for _, platform := range project.Platforms() {
			runner, err := CIRunner(platform)
			if err != nil {
				return CISchedule{}, err
			}
			platforms[platform] = true
			plan.Matrix = append(plan.Matrix, CIMatrixRow{
				Project: project.Name, Platform: platform, Runner: runner,
				Checks:   slices.Contains(project.CheckPlatforms, platform),
				Native:   slices.Contains(project.ReleasePlatforms, platform),
				Artifact: project.Name + "-" + platform,
			})
		}
	}
	if len(platforms) <= 1 {
		plan.Mode = "single"
		if len(platforms) == 1 {
			plan.Runner, _ = CIRunner(plan.Matrix[0].Platform)
		}
		plan.Matrix = []CIMatrixRow{}
	} else {
		plan.Mode = "matrix"
	}
	return plan, nil
}

// CIReleaseMode controls the release phase owned by an on-merge flow.
type CIReleaseMode string

const (
	CIReleaseAuto     CIReleaseMode = "auto"
	CIReleaseNone     CIReleaseMode = "none"
	CIReleaseGitHub   CIReleaseMode = "github"
	CIReleasePackages CIReleaseMode = "packages"
)

// ParseCIFlow validates a CI flow name.
func ParseCIFlow(value string) (CIFlow, error) {
	flow := CIFlow(value)
	switch flow {
	case CIFlowOnCommit, CIFlowOnMerge, CIFlowOnRelease, CIFlowOnPlatform:
		return flow, nil
	default:
		return "", fmt.Errorf("invalid CI flow %q: expected on-commit, on-merge, on-release, or on-platform", value)
	}
}

// ParseCIReleaseMode validates a flow release mode.
func ParseCIReleaseMode(value string) (CIReleaseMode, error) {
	mode := CIReleaseMode(value)
	switch mode {
	case CIReleaseAuto, CIReleaseNone, CIReleaseGitHub, CIReleasePackages:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid CI release mode %q: expected auto, none, github, or packages", value)
	}
}

// CIFlowOptions controls the narrow orchestration controls for a complete flow.
type CIFlowOptions struct {
	Project   string
	Platform  string
	OutputDir string
	RootOnly  bool
	SkipRoot  bool
}

// RunCIFlow executes one complete CI flow, including its guarded release phase.
func RunCIFlow(ctx context.Context, root string, flow CIFlow, environment string, releaseMode CIReleaseMode, stdout, stderr io.Writer) error {
	return RunCIFlowWithOptions(ctx, root, flow, environment, releaseMode, CIFlowOptions{}, stdout, stderr)
}

func RunCIFlowWithOptions(ctx context.Context, root string, flow CIFlow, environment string, releaseMode CIReleaseMode, options CIFlowOptions, stdout, stderr io.Writer) error {
	if options.RootOnly && (options.Project != "" || options.Platform != "" || options.OutputDir != "") {
		return errors.New("--root-only cannot be combined with project, platform, or output-dir")
	}
	if options.SkipRoot && options.RootOnly {
		return errors.New("--root-only and --skip-root cannot be used together")
	}
	if flow == CIFlowOnPlatform {
		return fmt.Errorf("%s is no longer a public flow; use --project and --platform", flow)
	}
	if options.Project != "" || options.Platform != "" || options.OutputDir != "" {
		if options.Project == "" || options.Platform == "" {
			return errors.New("--project and --platform are required for a scoped flow")
		}
		return runScopedCIFlow(ctx, root, flow, environment, releaseMode, options, stdout, stderr)
	}
	target, err := ciTarget(flow, environment)
	if err != nil {
		return err
	}
	runner := &commandCIRunner{
		stdout: stdout,
		stderr: stderr,
		mise: miseRunner{
			Stdout:  stdout,
			Stderr:  stderr,
			Ceiling: filepath.Dir(filepath.Clean(root)),
		},
	}
	if options.RootOnly {
		return runRootHook(ctx, filepath.Clean(root), flow, runner)
	}
	if !options.SkipRoot {
		if err := runRootHook(ctx, filepath.Clean(root), flow, runner); err != nil {
			return err
		}
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	projects, err := SelectCIProjects(root, manifest, flow)
	if err != nil {
		return err
	}
	eligible := 0
	for _, project := range projects {
		if !project.Eligible {
			fmt.Fprintf(stderr, "excluded %s: %s; use --project %s --platform <target>\n", project.Name, strings.Join(project.ExclusionReasons, "; "), project.Name)
			continue
		}
		eligible++
		if err := runSelectedProject(ctx, filepath.Clean(root), project, project.Platforms()[0], flow, target, "", runner); err != nil {
			return err
		}
	}
	if eligible == 0 {
		return errors.New("no eligible projects for the current host")
	}
	kind, err := DetectProjectKind(filepath.Clean(root))
	if err != nil {
		return err
	}
	if err := runCIReleasePhase(ctx, filepath.Clean(root), flow, releaseMode, kind, runner); err != nil {
		return err
	}
	return nil
}

func runRootHook(ctx context.Context, root string, flow CIFlow, runner ciRunner) error {
	exists, err := runner.TaskExists(ctx, root, string(flow))
	if err != nil {
		return fmt.Errorf("inspect root %s hook: %w", flow, err)
	}
	if !exists {
		return nil
	}
	if err := runner.Run(ctx, root, nil, "mise", "run", string(flow)); err != nil {
		return fmt.Errorf("run root %s hook: %w", flow, err)
	}
	return nil
}

func runScopedCIFlow(ctx context.Context, root string, flow CIFlow, environment string, _ CIReleaseMode, options CIFlowOptions, stdout, stderr io.Writer) error {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return err
	}
	projects, err := SelectCIProjects(root, manifest, flow)
	if err != nil {
		return err
	}
	var selected *CIPlatformProject
	for i := range projects {
		if projects[i].Name == options.Project {
			selected = &projects[i]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("unknown project %q", options.Project)
	}
	if !slices.Contains(selected.Platforms(), options.Platform) {
		return fmt.Errorf("platform %q is not declared for project %q", options.Platform, options.Project)
	}
	host, err := HostCIPlatform()
	if err != nil {
		return err
	}
	if host != options.Platform {
		return fmt.Errorf("platform %q requires %s host (current host is %s)", options.Platform, options.Platform, host)
	}
	if options.OutputDir != "" && !filepath.IsAbs(options.OutputDir) {
		return errors.New("output-dir must be an absolute path")
	}
	target, err := ciTarget(flow, environment)
	if err != nil {
		return err
	}
	runner := &commandCIRunner{stdout: stdout, stderr: stderr, mise: miseRunner{Stdout: stdout, Stderr: stderr, Ceiling: filepath.Dir(filepath.Clean(root))}}
	if !options.SkipRoot {
		if err := runRootHook(ctx, root, flow, runner); err != nil {
			return err
		}
	}
	if err := runSelectedProject(ctx, root, *selected, options.Platform, flow, target, options.OutputDir, runner); err != nil {
		return err
	}
	return nil
}

func runSelectedProject(ctx context.Context, root string, project CIPlatformProject, platform string, _ CIFlow, target, output string, runner ciRunner) error {
	directory := filepath.Join(root, project.Path)
	if project.Source == "registry" {
		if d, err := TemplateDirectory(root, project.Path); err == nil {
			directory = d
		} else {
			return err
		}
	}
	for _, args := range [][]string{{"install"}, {"run", "build"}, {"run", "test"}, {"run", "format:check"}, {"run", "lint"}} {
		if err := runner.Run(ctx, directory, nil, "mise", args...); err != nil {
			return fmt.Errorf("project %s platform %s: %w", project.Name, project.Platforms()[0], err)
		}
	}
	if project.Kind == "app" {
		for _, task := range []string{"deploy", "e2e"} {
			exists, err := runner.TaskExists(ctx, directory, task)
			if err != nil {
				return err
			}
			if exists {
				if err := runner.Run(ctx, directory, nil, "mise", "run", task, "--", target); err != nil {
					return err
				}
			}
		}
	}
	if slices.Contains(project.ReleasePlatforms, platform) && output != "" {
		if err := os.MkdirAll(output, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
		entries, err := os.ReadDir(output)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return errors.New("output-dir must be empty before native staging")
		}
		env := []string{"PREMISE_ARTIFACT_DIR=" + output}
		if err := runner.Run(ctx, directory, env, "mise", "run", "release:build"); err != nil {
			return fmt.Errorf("stage native artifacts: %w", err)
		}
		valid := false
		err = filepath.Walk(output, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				return nil
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return fmt.Errorf("invalid artifact %s", path)
			}
			valid = true
			return nil
		})
		if err != nil {
			return err
		}
		if !valid {
			return errors.New("native staging produced no artifacts")
		}
	}
	return nil
}

type ciRunner interface {
	TaskExists(context.Context, string, string) (bool, error)
	ShouldPublishRC(context.Context, string) (bool, error)
	PublishReleaseCandidate(context.Context, string, ProjectKind) error
	ReleaseStable(context.Context, string, CIReleaseMode) error
	Run(context.Context, string, []string, string, ...string) error
}

// commandCIRunner is private because it is the production adapter behind the
// testable ciRunner interface, not part of Premise's public API.
type commandCIRunner struct {
	stdout io.Writer
	stderr io.Writer
	mise   miseRunner
}

func (runner *commandCIRunner) TaskExists(ctx context.Context, directory, task string) (bool, error) {
	exists, err := runner.mise.taskExists(ctx, directory, task)
	if err != nil {
		return false, fmt.Errorf("inspect mise task %s: %w", task, err)
	}
	return exists, nil
}

func (runner *commandCIRunner) ShouldPublishRC(_ context.Context, root string) (bool, error) {
	if os.Getenv("GITHUB_EVENT_NAME") != "push" {
		return false, nil
	}
	ref := os.Getenv("GITHUB_REF")
	if !strings.HasPrefix(ref, "refs/heads/") || ref == "refs/heads/main" {
		return false, nil
	}
	repository, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open repository for RC decision: %w", err)
	}
	head, err := repository.Head()
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read HEAD for RC decision: %w", err)
	}
	commit, err := repository.CommitObject(head.Hash())
	if err != nil {
		return false, fmt.Errorf("read HEAD commit for RC decision: %w", err)
	}
	return strings.Contains(commit.Message, "[publish-rc]"), nil
}

func (runner *commandCIRunner) PublishReleaseCandidate(ctx context.Context, root string, kind ProjectKind) error {
	return publishReleaseCandidate(ctx, root, kind, runner.stdout, runner.stderr)
}

func (runner *commandCIRunner) ReleaseStable(ctx context.Context, root string, mode CIReleaseMode) error {
	switch mode {
	case CIReleaseNone:
		return nil
	case CIReleaseAuto:
		_, err := PublishStableRelease(ctx, root, runner.stdout, runner.stderr)
		return err
	case CIReleaseGitHub:
		plan, err := PrepareStableRelease(ctx, root, runner.stdout)
		if err != nil || plan.Skip {
			return err
		}
		_, err = PublishGitHubRelease(ctx, root, runner.stdout, runner.stderr)
		return err
	case CIReleasePackages:
		_, err := PublishLanguagePackages(ctx, root, runner.stdout, runner.stderr)
		return err
	default:
		return fmt.Errorf("unsupported CI release mode %q", mode)
	}
}

func (runner *commandCIRunner) Run(ctx context.Context, directory string, environment []string, name string, arguments ...string) error {
	if name == "mise" {
		if err := runner.mise.run(ctx, directory, environment, arguments...); err != nil {
			return fmt.Errorf("run mise %s: %w", strings.Join(arguments, " "), err)
		}
		return nil
	}
	if name != "pm" {
		return fmt.Errorf("unsupported CI command %q", name)
	}
	command := exec.CommandContext(ctx, "pm", arguments...)
	command.Dir = directory
	command.Env = runner.mise.environment(environment)
	command.Stdout = runner.stdout
	command.Stderr = runner.stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run pm %s: %w", strings.Join(arguments, " "), err)
	}
	return nil
}

type ciTask struct {
	name     string
	optional bool
	appOnly  bool
	target   string
}

func runCIFlow(ctx context.Context, root string, flow CIFlow, target string, releaseMode CIReleaseMode, runner ciRunner) error {
	if flow == CIFlowOnMerge && releaseMode == CIReleasePackages {
		return runner.ReleaseStable(ctx, root, releaseMode)
	}
	override, err := runner.TaskExists(ctx, root, string(flow))
	if err != nil {
		return fmt.Errorf("inspect %s override: %w", flow, err)
	}
	var kind ProjectKind
	if override {
		arguments := []string{"run", string(flow)}
		if flow == CIFlowOnRelease {
			arguments = append(arguments, "--", target)
		}
		if err := runner.Run(ctx, root, nil, "mise", arguments...); err != nil {
			return fmt.Errorf("run %s override: %w", flow, err)
		}
	} else {
		kind, err = DetectProjectKind(root)
		if err != nil {
			return fmt.Errorf("detect CI project kind: %w", err)
		}
		switch kind {
		case ProjectKindMonorepo:
			err = runMonorepoCIFlow(ctx, root, flow, target, runner)
		case ProjectKindTemplateRegistry:
			err = runRegistryCIFlow(ctx, root, flow, target, runner)
		default:
			err = fmt.Errorf("unsupported CI project kind %q", kind)
		}
		if err != nil {
			return err
		}
	}
	return runCIReleasePhase(ctx, root, flow, releaseMode, kind, runner)
}

func runMonorepoCIFlow(ctx context.Context, root string, flow CIFlow, target string, runner ciRunner) error {
	if err := runner.Run(ctx, root, nil, "pm", "install"); err != nil {
		return fmt.Errorf("install monorepo: %w", err)
	}
	for _, task := range ciTasks(flow, target) {
		selector := "//...:" + task.name
		if task.optional {
			exists, err := runner.TaskExists(ctx, root, selector)
			if err != nil {
				return fmt.Errorf("inspect optional task %s: %w", task.name, err)
			}
			if !exists {
				continue
			}
		}
		if err := runCIMiseTask(ctx, runner, root, selector, task); err != nil {
			return fmt.Errorf("run monorepo task %s: %w", task.name, err)
		}
	}
	return nil
}

func runRegistryCIFlow(ctx context.Context, root string, flow CIFlow, target string, runner ciRunner) error {
	registry, err := LoadRegistry(root)
	if err != nil {
		return fmt.Errorf("load template registry: %w", err)
	}
	var failures []error
	for _, template := range registry.Templates {
		if err := runRegistryTemplateCIFlow(ctx, root, template, flow, target, runner); err != nil {
			failures = append(failures, fmt.Errorf("template %s: %w", template.Name, err))
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("template registry CI failures: %w", err)
	}
	return nil
}

func runRegistryTemplateCIFlow(ctx context.Context, root string, template Template, flow CIFlow, target string, runner ciRunner) error {
	directory, err := TemplateDirectory(root, template.Path)
	if err != nil {
		return err
	}
	if err := runner.Run(ctx, directory, nil, "mise", "install"); err != nil {
		return fmt.Errorf("install tools: %w", err)
	}
	install := ciTask{name: "install"}
	if err := runCIMiseTask(ctx, runner, directory, install.name, install); err != nil {
		return fmt.Errorf("run task install: %w", err)
	}
	for _, task := range ciTasks(flow, target) {
		if task.appOnly && template.Kind != "app" {
			continue
		}
		if task.optional {
			exists, err := runner.TaskExists(ctx, directory, task.name)
			if err != nil {
				return fmt.Errorf("inspect task %s: %w", task.name, err)
			}
			if !exists {
				continue
			}
		}
		if err := runCIMiseTask(ctx, runner, directory, task.name, task); err != nil {
			return fmt.Errorf("run task %s: %w", task.name, err)
		}
	}
	return nil
}

func runCIReleasePhase(ctx context.Context, root string, flow CIFlow, releaseMode CIReleaseMode, kind ProjectKind, runner ciRunner) error {
	switch flow {
	case CIFlowOnCommit:
		if releaseMode == CIReleaseNone {
			return nil
		}
		if releaseMode != CIReleaseAuto {
			return fmt.Errorf("%s only supports auto or none release mode", flow)
		}
		publish, err := runner.ShouldPublishRC(ctx, root)
		if err != nil {
			return fmt.Errorf("decide RC publication: %w", err)
		}
		if !publish {
			return nil
		}
		return runner.PublishReleaseCandidate(ctx, root, kind)
	case CIFlowOnMerge:
		return runner.ReleaseStable(ctx, root, releaseMode)
	case CIFlowOnRelease:
		if releaseMode != CIReleaseAuto && releaseMode != CIReleaseNone {
			return fmt.Errorf("%s only supports auto or none release mode", flow)
		}
		return nil
	default:
		return fmt.Errorf("unsupported CI flow %q", flow)
	}
}

func runCIMiseTask(ctx context.Context, runner ciRunner, directory, selector string, task ciTask) error {
	arguments := []string{"run", "--jobs", "1", selector}
	if task.target != "" {
		arguments = append(arguments, "--", task.target)
	}
	return runner.Run(ctx, directory, nil, "mise", arguments...)
}

func ciTasks(flow CIFlow, target string) []ciTask {
	switch flow {
	case CIFlowOnCommit:
		return []ciTask{
			{name: "build"},
			{name: "test"},
			{name: "format"},
			{name: "format:check"},
			{name: "lint"},
			{name: "deploy", optional: true, appOnly: true, target: target},
			{name: "e2e", optional: true, appOnly: true, target: target},
		}
	case CIFlowOnMerge:
		return []ciTask{
			{name: "build"},
			{name: "test"},
			{name: "format"},
			{name: "format:check"},
			{name: "lint"},
			{name: "deploy", optional: true, appOnly: true, target: target},
			{name: "e2e", optional: true, appOnly: true, target: target},
		}
	case CIFlowOnRelease:
		return []ciTask{
			{name: "deploy", optional: true, appOnly: true, target: target},
			{name: "e2e", optional: true, appOnly: true, target: target},
		}
	default:
		return nil
	}
}

func ciTarget(flow CIFlow, environment string) (string, error) {
	switch flow {
	case CIFlowOnCommit:
		if environment != "" {
			return "", fmt.Errorf("%s does not accept a deployment environment", flow)
		}
		return "preview", nil
	case CIFlowOnMerge:
		if environment != "" {
			return "", fmt.Errorf("%s does not accept a deployment environment", flow)
		}
		return "dev", nil
	case CIFlowOnRelease:
		if environment == "" {
			environment = "stage"
		}
		if environment != "stage" && environment != "prod" {
			return "", fmt.Errorf("invalid release environment %q: expected stage or prod", environment)
		}
		return environment, nil
	default:
		return "", fmt.Errorf("unsupported CI flow %q", flow)
	}
}
