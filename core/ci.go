package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	misecmd "github.com/cloudvoyant/premise/internal/mise"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CIFlow names one convention-driven CI lifecycle.
type CIFlow string

const (
	CIFlowOnCommit  CIFlow = "on-commit"
	CIFlowOnMerge   CIFlow = "on-merge"
	CIFlowOnRelease CIFlow = "on-release"
)

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
	case CIFlowOnCommit, CIFlowOnMerge, CIFlowOnRelease:
		return flow, nil
	default:
		return "", fmt.Errorf("invalid CI flow %q: expected on-commit, on-merge, or on-release", value)
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

// RunCIFlow executes one complete CI flow, including its guarded release phase.
func RunCIFlow(ctx context.Context, root string, flow CIFlow, environment string, releaseMode CIReleaseMode, stdout, stderr io.Writer) error {
	target, err := ciTarget(flow, environment)
	if err != nil {
		return err
	}
	runner := &commandCIRunner{
		stdout: stdout,
		stderr: stderr,
		mise: misecmd.Runner{
			Stdout:  stdout,
			Stderr:  stderr,
			Ceiling: filepath.Dir(filepath.Clean(root)),
		},
	}
	return runCIFlow(ctx, filepath.Clean(root), flow, target, releaseMode, runner)
}

type ciRunner interface {
	TaskExists(context.Context, string, string) (bool, error)
	ShouldPublishRC(context.Context, string) (bool, error)
	ReleaseStable(context.Context, string, CIReleaseMode) error
	Run(context.Context, string, []string, string, ...string) error
}

type commandCIRunner struct {
	stdout io.Writer
	stderr io.Writer
	mise   misecmd.Runner
}

func (runner *commandCIRunner) TaskExists(ctx context.Context, directory, task string) (bool, error) {
	exists, err := runner.mise.TaskExists(ctx, directory, task)
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
		if err := runner.mise.Run(ctx, directory, environment, arguments...); err != nil {
			return fmt.Errorf("run mise %s: %w", strings.Join(arguments, " "), err)
		}
		return nil
	}
	if name != "pm" {
		return fmt.Errorf("unsupported CI command %q", name)
	}
	command := exec.CommandContext(ctx, "pm", arguments...)
	command.Dir = directory
	command.Env = runner.mise.Environment(environment)
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
	directory, err := TemplateDirectory(root, template.Name)
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
		exists, err := runner.TaskExists(ctx, root, "publish:rc")
		if err != nil {
			return fmt.Errorf("inspect publish:rc task: %w", err)
		}
		environment, err := releaseCandidateEnvironment(root)
		if err != nil {
			return err
		}
		if exists {
			if err := runner.Run(ctx, root, environment, "mise", "run", "publish:rc"); err != nil {
				return fmt.Errorf("publish RC: %w", err)
			}
			return nil
		}
		if kind == "" {
			kind, err = DetectProjectKind(root)
			if err != nil {
				return fmt.Errorf("detect RC project kind: %w", err)
			}
		}
		if kind != ProjectKindMonorepo {
			return errors.New("marked RC push requires a root publish:rc task")
		}
		return runner.Run(ctx, root, environment, "mise", "run", "--jobs", "1", "//...:publish:rc")
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
			{name: "lint"},
			{name: "deploy", optional: true, appOnly: true, target: target},
			{name: "e2e", optional: true, appOnly: true, target: target},
		}
	case CIFlowOnMerge:
		return []ciTask{
			{name: "build"},
			{name: "test"},
			{name: "format"},
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
