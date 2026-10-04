package core

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type ciCall struct {
	directory   string
	environment []string
	name        string
	arguments   []string
}

type fakeCIRunner struct {
	tasks                 map[string]bool
	taskErrs              map[string]error
	failures              map[string]error
	publishRC             bool
	publishRCErr          error
	releaseCandidateKinds []ProjectKind
	releaseModes          []CIReleaseMode
	calls                 []ciCall
}

func (runner *fakeCIRunner) TaskExists(_ context.Context, directory, task string) (bool, error) {
	key := filepath.Clean(directory) + "|" + task
	if err := runner.taskErrs[key]; err != nil {
		return false, err
	}
	return runner.tasks[key], nil
}

func (runner *fakeCIRunner) ShouldPublishRC(_ context.Context, _ string) (bool, error) {
	return runner.publishRC, runner.publishRCErr
}

func (runner *fakeCIRunner) PublishReleaseCandidate(_ context.Context, _ string, kind ProjectKind) error {
	runner.releaseCandidateKinds = append(runner.releaseCandidateKinds, kind)
	return nil
}

func (runner *fakeCIRunner) ReleaseStable(_ context.Context, _ string, mode CIReleaseMode) error {
	runner.releaseModes = append(runner.releaseModes, mode)
	return nil
}

func (runner *fakeCIRunner) Run(_ context.Context, directory string, environment []string, name string, arguments ...string) error {
	call := ciCall{
		directory:   filepath.Clean(directory),
		environment: append([]string{}, environment...),
		name:        name,
		arguments:   append([]string{}, arguments...),
	}
	runner.calls = append(runner.calls, call)
	key := name + " " + strings.Join(arguments, " ")
	if err := runner.failures[filepath.Clean(directory)+"|"+key]; err != nil {
		return err
	}
	return runner.failures[key]
}

func newFakeCIRunner() *fakeCIRunner {
	return &fakeCIRunner{
		tasks:    map[string]bool{},
		taskErrs: map[string]error{},
		failures: map[string]error{},
	}
}

func TestParseCIFlow(t *testing.T) {
	for _, value := range []string{"on-commit", "on-merge", "on-release"} {
		flow, err := ParseCIFlow(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(flow) != value {
			t.Fatalf("ParseCIFlow(%q) = %q", value, flow)
		}
	}
	if _, err := ParseCIFlow("feature"); err == nil || !strings.Contains(err.Error(), "expected on-commit") {
		t.Fatalf("ParseCIFlow(feature) error = %v", err)
	}
}

func TestCITargets(t *testing.T) {
	for _, test := range []struct {
		flow        CIFlow
		environment string
		want        string
		wantErr     bool
	}{
		{flow: CIFlowOnCommit, want: "preview"},
		{flow: CIFlowOnMerge, want: "dev"},
		{flow: CIFlowOnRelease, want: "stage"},
		{flow: CIFlowOnRelease, environment: "prod", want: "prod"},
		{flow: CIFlowOnCommit, environment: "stage", wantErr: true},
		{flow: CIFlowOnRelease, environment: "preview", wantErr: true},
	} {
		got, err := ciTarget(test.flow, test.environment)
		if test.wantErr {
			if err == nil {
				t.Fatalf("ciTarget(%q, %q) unexpectedly succeeded", test.flow, test.environment)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("ciTarget(%q, %q) = %q, want %q", test.flow, test.environment, got, test.want)
		}
	}
}

func TestCIFlowRootOverridePrecedesProjectClassification(t *testing.T) {
	root := writeCIProject(t, []Template{templateFixture("app", "app")}, true)
	runner := newFakeCIRunner()
	runner.tasks[root+"|on-commit"] = true

	if err := runCIFlow(context.Background(), root, CIFlowOnCommit, "preview", CIReleaseNone, runner); err != nil {
		t.Fatal(err)
	}
	want := []ciCall{{directory: root, environment: []string{}, name: "mise", arguments: []string{"run", "on-commit"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("override calls = %#v, want %#v", runner.calls, want)
	}
}

func TestOnMergeFlowOwnsStableReleaseAfterOverride(t *testing.T) {
	root := writeCIProject(t, nil, true)
	runner := newFakeCIRunner()
	runner.tasks[root+"|on-merge"] = true

	if err := runCIFlow(context.Background(), root, CIFlowOnMerge, "dev", CIReleaseAuto, runner); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.releaseModes, []CIReleaseMode{CIReleaseAuto}) {
		t.Fatalf("release modes = %#v, want auto", runner.releaseModes)
	}
}

func TestPackagesReleasePhaseSkipsLifecycle(t *testing.T) {
	root := writeCIProject(t, nil, true)
	runner := newFakeCIRunner()

	if err := runCIFlow(context.Background(), root, CIFlowOnMerge, "dev", CIReleasePackages, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("packages release ran lifecycle: %#v", runner.calls)
	}
	if !reflect.DeepEqual(runner.releaseModes, []CIReleaseMode{CIReleasePackages}) {
		t.Fatalf("release modes = %#v, want packages", runner.releaseModes)
	}
}

func TestCIFlowOverrideFailureDoesNotFallBack(t *testing.T) {
	root := writeCIProject(t, nil, true)
	runner := newFakeCIRunner()
	runner.tasks[root+"|on-merge"] = true
	runner.failures["mise run on-merge"] = errors.New("override failed")

	err := runCIFlow(context.Background(), root, CIFlowOnMerge, "dev", CIReleaseNone, runner)
	if err == nil || !strings.Contains(err.Error(), "override failed") {
		t.Fatalf("runCIFlow() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("override failure ran %d commands, want 1", len(runner.calls))
	}
}

func TestMonorepoCIFlowUsesLifecycleOrderAndPublishesMarkedRC(t *testing.T) {
	root := writeCIProject(t, nil, true)
	runner := newFakeCIRunner()
	runner.publishRC = true

	if err := runCIFlow(context.Background(), root, CIFlowOnCommit, "preview", CIReleaseAuto, runner); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, call := range runner.calls {
		line := call.name + " " + strings.Join(call.arguments, " ")
		if len(call.environment) > 0 {
			line += " [" + strings.Join(call.environment, ",") + "]"
		}
		got = append(got, line)
	}
	want := []string{
		"pm install",
		"mise run --jobs 1 //...:build",
		"mise run --jobs 1 //...:test",
		"mise run --jobs 1 //...:format",
		"mise run --jobs 1 //...:format:check",
		"mise run --jobs 1 //...:lint",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("monorepo calls = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(runner.releaseCandidateKinds, []ProjectKind{ProjectKindMonorepo}) {
		t.Fatalf("RC project kinds = %#v, want monorepo", runner.releaseCandidateKinds)
	}
}

func TestShouldPublishRCRequiresPushMarkerOutsideMain(t *testing.T) {
	root := t.TempDir()
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("feat: publish candidate [publish-rc]", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "test@example.com", When: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	runner := &commandCIRunner{}
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", "refs/heads/feature/test")
	publish, err := runner.ShouldPublishRC(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !publish {
		t.Fatal("marked feature push did not enable RC publication")
	}
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	if publish, err := runner.ShouldPublishRC(context.Background(), root); err != nil || publish {
		t.Fatalf("pull request RC decision = %v, %v; want false", publish, err)
	}
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	if publish, err := runner.ShouldPublishRC(context.Background(), root); err != nil || publish {
		t.Fatalf("main RC decision = %v, %v; want false", publish, err)
	}
	t.Setenv("GITHUB_EVENT_NAME", "")
	t.Setenv("GITHUB_REF", "refs/heads/feature/test")
	if publish, err := runner.ShouldPublishRC(context.Background(), root); err != nil || publish {
		t.Fatalf("missing event RC decision = %v, %v; want false", publish, err)
	}
	t.Setenv("GITHUB_EVENT_NAME", "push")
	t.Setenv("GITHUB_REF", "refs/tags/v1.2.3")
	if publish, err := runner.ShouldPublishRC(context.Background(), root); err != nil || publish {
		t.Fatalf("tag push RC decision = %v, %v; want false", publish, err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("unmarked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("docs: no candidate", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "test@example.com", When: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_REF", "refs/heads/feature/test")
	if publish, err := runner.ShouldPublishRC(context.Background(), root); err != nil || publish {
		t.Fatalf("unmarked push RC decision = %v, %v; want false", publish, err)
	}
}

func TestOnCommitWithoutMarkerDoesNotPublishRC(t *testing.T) {
	root := writeCIProject(t, nil, true)
	runner := newFakeCIRunner()
	runner.tasks[root+"|publish:rc"] = true

	if err := runCIFlow(context.Background(), root, CIFlowOnCommit, "preview", CIReleaseAuto, runner); err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if strings.Join(call.arguments, " ") == "run publish:rc" {
			t.Fatal("on-commit published RC without the marker decision")
		}
	}
}

func TestRegistryCIFlowRunsEachTemplateAndSkipsAppTasksForLibraries(t *testing.T) {
	app := templateFixture("z-app", "app")
	library := templateFixture("a-lib", "lib")
	root := writeCIProject(t, []Template{app, library}, false)
	for _, template := range []Template{app, library} {
		if err := os.MkdirAll(filepath.Join(root, "templates", template.Name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := newFakeCIRunner()
	appDirectory := filepath.Join(root, "templates", app.Name)
	runner.tasks[appDirectory+"|deploy"] = true
	runner.tasks[appDirectory+"|e2e"] = true

	if err := runCIFlow(context.Background(), root, CIFlowOnCommit, "preview", CIReleaseNone, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) == 0 || runner.calls[0].directory != filepath.Join(root, "templates", library.Name) {
		t.Fatalf("registry did not run templates in name order: %#v", runner.calls)
	}
	var libraryAppTask bool
	var appDeploy bool
	for _, call := range runner.calls {
		joined := strings.Join(call.arguments, " ")
		if call.directory == filepath.Join(root, "templates", library.Name) && (strings.Contains(joined, "deploy") || strings.Contains(joined, "e2e")) {
			libraryAppTask = true
		}
		if call.directory == appDirectory && joined == "run --jobs 1 deploy -- preview" {
			appDeploy = true
		}
	}
	if libraryAppTask {
		t.Fatal("library template ran an app-only task")
	}
	if !appDeploy {
		t.Fatal("app template did not run deploy preview")
	}
	firstAppCall := -1
	for index, call := range runner.calls {
		if call.directory == appDirectory {
			firstAppCall = index
			break
		}
	}
	if firstAppCall < 0 || runner.calls[firstAppCall].name != "mise" || !reflect.DeepEqual(runner.calls[firstAppCall].arguments, []string{"install"}) {
		t.Fatalf("first app command = %#v, want mise install", runner.calls[firstAppCall])
	}
}

func TestRegistryCIStopsFailedTemplateBeforeDeployAndContinues(t *testing.T) {
	app := templateFixture("a-app", "app")
	library := templateFixture("z-lib", "lib")
	root := writeCIProject(t, []Template{app, library}, false)
	for _, template := range []Template{app, library} {
		if err := os.MkdirAll(filepath.Join(root, "templates", template.Name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runner := newFakeCIRunner()
	appDirectory := filepath.Join(root, "templates", app.Name)
	libraryDirectory := filepath.Join(root, "templates", library.Name)
	runner.tasks[appDirectory+"|deploy"] = true
	runner.failures[appDirectory+"|mise run --jobs 1 build"] = errors.New("build failed")

	err := runCIFlow(context.Background(), root, CIFlowOnCommit, "preview", CIReleaseNone, runner)
	if err == nil || !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("runCIFlow() error = %v", err)
	}
	var appDeploy, continued bool
	for _, call := range runner.calls {
		joined := strings.Join(call.arguments, " ")
		if call.directory == appDirectory && strings.Contains(joined, "deploy") {
			appDeploy = true
		}
		if call.directory == libraryDirectory {
			continued = true
		}
	}
	if appDeploy {
		t.Fatal("failed app template proceeded to deployment")
	}
	if !continued {
		t.Fatal("registry stopped before validating the next template")
	}
}

func TestRegistryCIUsesFormattingContract(t *testing.T) {
	template := templateFixture("app", "app")
	root := writeCIProject(t, []Template{template}, false)
	directory := filepath.Join(root, "templates", template.Name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := newFakeCIRunner()

	if err := runCIFlow(context.Background(), root, CIFlowOnCommit, "preview", CIReleaseNone, runner); err != nil {
		t.Fatal(err)
	}

	var lifecycle []string
	for _, call := range runner.calls {
		if call.directory == directory {
			lifecycle = append(lifecycle, strings.Join(call.arguments, " "))
		}
	}
	want := []string{
		"install",
		"run --jobs 1 install",
		"run --jobs 1 build",
		"run --jobs 1 test",
		"run --jobs 1 format",
		"run --jobs 1 format:check",
		"run --jobs 1 lint",
	}
	if !reflect.DeepEqual(lifecycle, want) {
		t.Fatalf("registry lifecycle = %#v, want format contract %#v", lifecycle, want)
	}
}

func TestCIFlowAllowsTemplatesInMonorepoLifecycle(t *testing.T) {
	root := writeCIProject(t, []Template{templateFixture("app", "app")}, true)
	runner := newFakeCIRunner()
	if err := runCIFlow(context.Background(), root, CIFlowOnCommit, "preview", CIReleaseNone, runner); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) == 0 || runner.calls[0].name != "pm" || strings.Join(runner.calls[0].arguments, " ") != "install" {
		t.Fatalf("hybrid workspace did not run monorepo lifecycle: %#v", runner.calls)
	}
	for _, call := range runner.calls {
		if strings.Contains(call.directory, filepath.Join("templates", "app")) {
			t.Fatalf("monorepo lifecycle unexpectedly ran registry fallback: %#v", call)
		}
	}
}

func TestCIStagingValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		flow    CIFlow
		options CIFlowOptions
		want    string
	}{
		{name: "channel requires version", flow: CIFlowOnCommit, options: CIFlowOptions{Channel: "rc", OutputDir: t.TempDir()}, want: "provided together"},
		{name: "version requires channel", flow: CIFlowOnCommit, options: CIFlowOptions{Version: "v1.0.0-rc.1", OutputDir: t.TempDir()}, want: "provided together"},
		{name: "absolute output", flow: CIFlowOnCommit, options: CIFlowOptions{OutputDir: "relative"}, want: "absolute"},
		{name: "channel mismatch", flow: CIFlowOnCommit, options: CIFlowOptions{Channel: "stable", Version: "v1.0.0", OutputDir: t.TempDir()}, want: "does not match"},
		{name: "version mismatch", flow: CIFlowOnCommit, options: CIFlowOptions{Channel: "rc", Version: "nonsense", OutputDir: t.TempDir()}, want: "invalid release version"},
		{name: "valid rc", flow: CIFlowOnCommit, options: CIFlowOptions{Channel: "rc", Version: "v1.0.0-rc.1", OutputDir: t.TempDir()}},
		{name: "valid stable", flow: CIFlowOnMerge, options: CIFlowOptions{Channel: "stable", Version: "v1.0.0", OutputDir: t.TempDir()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateCIStaging(test.flow, test.options)
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCIFlowRejectsDirectPublication(t *testing.T) {
	for _, flow := range []CIFlow{CIFlowOnCommit, CIFlowOnMerge} {
		err := RunCIFlowWithOptions(t.Context(), t.TempDir(), flow, "", CIReleaseAuto, CIFlowOptions{}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "cannot publish directly") {
			t.Fatalf("flow %s: error = %v", flow, err)
		}
	}
}

func TestCIOutputRejectsStaleFiles(t *testing.T) {
	root := t.TempDir()
	if err := prepareCIOutput(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stale.msi"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareCIOutput(root); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanCIScheduleUsesOneMatrixForAllProjects(t *testing.T) {
	projects := []CIPlatformProject{
		{Name: "lib", CheckPlatforms: []string{"linux"}, ReleasePlatforms: []string{}},
		{Name: "tauri", CheckPlatforms: []string{"linux", "macos", "windows"}, ReleasePlatforms: []string{"linux", "macos", "windows"}},
	}
	plan, err := PlanCISchedule(projects)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "matrix" || !plan.HasNative || len(plan.Matrix) != 4 {
		t.Fatalf("schedule = %#v, want four rows in one matrix", plan)
	}
	if plan.Matrix[0].Project != "lib" || plan.Matrix[0].Platform != "linux" || plan.Matrix[0].Native {
		t.Fatalf("single-platform project omitted from matrix: %#v", plan.Matrix[0])
	}
	if plan.Matrix[2].Runner != "macos-14" || plan.Matrix[3].Runner != "windows-2022" {
		t.Fatalf("wrong runner images: %#v", plan.Matrix)
	}
}

func TestPlanCIScheduleSingleRunnerAndNoNative(t *testing.T) {
	plan, err := PlanCISchedule([]CIPlatformProject{{Name: "app", CheckPlatforms: []string{"macos"}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "single" || plan.Runner != "macos-14" || plan.HasNative || len(plan.Matrix) != 0 {
		t.Fatalf("single schedule = %#v", plan)
	}
	if _, err := PlanCISchedule(nil); err == nil {
		t.Fatal("empty project schedule accepted")
	}
}

func writeCIProject(t *testing.T, templates []Template, monorepo bool) string {
	t.Helper()
	root := t.TempDir()
	manifest := NewManifest("ci-fixture")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: templates}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	mise := "[tasks.build]\nrun = 'echo build'\n"
	if monorepo {
		mise = "monorepo_root = true\n" + mise
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte(mise), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}
