package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

func TestPlanStableReleaseSupportsTaglessRepository(t *testing.T) {
	root := t.TempDir()
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("feat: first release", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanStableRelease(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if plan != (ReleasePlan{Version: "v0.1.0"}) {
		t.Fatalf("PlanStableRelease() = %#v, want v0.1.0", plan)
	}
}

func TestPlanStableReleaseCreatesAndReusesVersion(t *testing.T) {
	root := t.TempDir()
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	author := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}
	tracked := filepath.Join(root, "README.md")
	if err := os.WriteFile(tracked, []byte("bootstrap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := worktree.Commit("chore: bootstrap", &git.CommitOptions{Author: author})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateTag("v0.0.0", bootstrap, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tracked, []byte("bootstrap\nfeature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	head, err := worktree.Commit("feat: release planning", &git.CommitOptions{Author: author})
	if err != nil {
		t.Fatal(err)
	}

	plan, err := PlanStableRelease(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v0.1.0" || plan.ReuseTag || plan.Skip {
		t.Fatalf("PlanStableRelease() = %#v", plan)
	}
	if _, err := repository.CreateTag("v0.1.0", head, nil); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanStableRelease(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v0.1.0" || !plan.ReuseTag || plan.Skip {
		t.Fatalf("PlanStableRelease() reuse = %#v", plan)
	}
}

func TestPublishStableReleaseOrdersGitHubBeforeCargo(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	var calls []string
	useTestPackageManagers(t, testPackageManager{
		id: "cargo",
		publish: func(_ context.Context, _ string, version, task string, _, _ io.Writer) error {
			if version != "v1.2.3" || task != "publish" {
				t.Fatalf("Cargo publication: %q %q", version, task)
			}
			calls = append(calls, "cargo")
			return nil
		},
	})
	originalGoReleaser := executeGoReleaser
	defer func() { executeGoReleaser = originalGoReleaser }()
	executeGoReleaser = func(_ context.Context, _ string, plugins []PackageManagerBackend, snapshot bool, _, _ io.Writer) error {
		if len(plugins) != 1 || plugins[0].ID() != "cargo" || snapshot {
			t.Fatalf("unexpected GoReleaser arguments: plugins=%v snapshot=%v", plugins, snapshot)
		}
		calls = append(calls, "github")
		return nil
	}

	plan, err := PublishStableRelease(t.Context(), root, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v1.2.3" || !plan.ReuseTag {
		t.Fatalf("PublishStableRelease() plan = %#v", plan)
	}
	if got := strings.Join(calls, ","); got != "github,cargo" {
		t.Fatalf("release call order = %q", got)
	}

	calls = nil
	publishErr := errors.New("GitHub publication failed")
	executeGoReleaser = func(_ context.Context, _ string, _ []PackageManagerBackend, _ bool, _, _ io.Writer) error {
		calls = append(calls, "github")
		return publishErr
	}
	if _, err := PublishStableRelease(t.Context(), root, io.Discard, io.Discard); !errors.Is(err, publishErr) {
		t.Fatalf("PublishStableRelease() error = %v, want %v", err, publishErr)
	}
	if got := strings.Join(calls, ","); got != "github" {
		t.Fatalf("failed release call order = %q; Cargo must not run", got)
	}
}

func writeTaggedCargoReleaseFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	manifest := NewManifest("premise-cargo")
	manifest.Workspace.PackageManagers = []string{"cargo"}
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{templateFixture("premise-rust-app", "app")}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	appRoot := filepath.Join(root, "templates", "premise-rust-app")
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, "Cargo.toml"), []byte("[package]\nname = \"premise-rust-app\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := worktree.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("feat: release", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "test@example.com", When: time.Now(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := repository.CreateTag("v1.2.3", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reference.Name().Short() != "v1.2.3" {
		t.Fatalf("release tag = %q", reference.Name().Short())
	}
	return root
}

func TestPreflightFailurePreventsPushingReleaseTag(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	repository, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteTag("v1.2.3"); err != nil {
		t.Fatal(err)
	}
	preflightErr := errors.New("package preflight failed")
	useTestPackageManagers(t, testPackageManager{
		id:        "cargo",
		publish:   func(context.Context, string, string, string, io.Writer, io.Writer) error { return nil },
		preflight: func(context.Context, string, string, string) error { return preflightErr },
	})
	originalPush := pushReleaseTag
	defer func() { pushReleaseTag = originalPush }()
	pushed := false
	pushReleaseTag = func(context.Context, string, string, transport.AuthMethod) error {
		pushed = true
		return nil
	}
	if _, err := prepareStableRelease(t.Context(), root, io.Discard, true); !errors.Is(err, preflightErr) {
		t.Fatalf("prepareStableRelease() error = %v, want %v", err, preflightErr)
	}
	if pushed {
		t.Fatal("release tag was pushed after publication preflight failed")
	}
}

func TestArtifactBuildFailurePreventsPushingReleaseTag(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	repository, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteTag("v1.2.3"); err != nil {
		t.Fatal(err)
	}
	useTestPackageManagers(t, testPackageManager{id: "cargo", builds: "builds:\n  - id: app\n"})
	originalGoReleaser := executeGoReleaser
	defer func() { executeGoReleaser = originalGoReleaser }()
	buildErr := errors.New("cross-build failed")
	executeGoReleaser = func(_ context.Context, _ string, _ []PackageManagerBackend, snapshot bool, _, _ io.Writer) error {
		if !snapshot {
			t.Fatal("GoReleaser published after the preflight build failed")
		}
		return buildErr
	}
	originalPush := pushReleaseTag
	defer func() { pushReleaseTag = originalPush }()
	pushReleaseTag = func(context.Context, string, string, transport.AuthMethod) error {
		t.Fatal("release tag was pushed after the artifact build failed")
		return nil
	}
	if _, err := PublishStableRelease(t.Context(), root, io.Discard, io.Discard); !errors.Is(err, buildErr) {
		t.Fatalf("PublishStableRelease() error = %v, want %v", err, buildErr)
	}
}

func TestCreateAndPushReleaseTagRollsBackLocalTagOnFailure(t *testing.T) {
	root := t.TempDir()
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(root, "README.md")
	if err := os.WriteFile(tracked, []byte("release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("feat: release", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "test@example.com", When: time.Now(),
	}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "test-token")
	if err := createAndPushReleaseTag(t.Context(), root, "v1.0.0", releaseAuthentication()); err == nil {
		t.Fatal("createAndPushReleaseTag() succeeded without an origin remote")
	}
	if _, err := repository.Reference("refs/tags/v1.0.0", true); !errors.Is(err, plumbing.ErrReferenceNotFound) {
		t.Fatalf("local release tag was not rolled back: %v", err)
	}
}

func TestCreateAndPushReleaseCandidateTagRollsBackOnFailure(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	repository, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "test-token")
	version := "v1.2.4-rc.123"
	err = createAndPushReleaseTag(t.Context(), root, version, releaseAuthentication())
	if err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("RC tag push without origin = %v, want missing remote (not a version rejection)", err)
	}
	if _, err := repository.Reference(plumbing.ReferenceName("refs/tags/"+version), true); !errors.Is(err, plumbing.ErrReferenceNotFound) {
		t.Fatalf("local RC tag was not rolled back: %v", err)
	}
}

func TestPublishReleaseValidatesChannelVersionBeforeTags(t *testing.T) {
	for _, tc := range []struct{ channel, version string }{
		{"stable", "v1.2.3-rc.42"}, {"rc", "v1.2.3"}, {"rc", "v1.2.3-beta.1"},
	} {
		_, err := PublishRelease(t.Context(), t.TempDir(), ReleasePublishOptions{
			Channel: tc.channel, ExpectedVersion: tc.version,
		}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Errorf("%s %s error = %v, want invalid version", tc.channel, tc.version, err)
		}
	}
}

func TestGoReleaserAcceptsGHToGitHubTokenAlias(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "alias-token")
	if got := goreleaserToken(); got != "alias-token" {
		t.Fatalf("GoReleaser alias = %q", got)
	}
	t.Setenv("GITHUB_TOKEN", "preferred-token")
	if got := goreleaserToken(); got != "preferred-token" {
		t.Fatalf("GoReleaser preferred token = %q", got)
	}
}

func TestPublishReleaseRejectsMissingCredentialBeforeTagging(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	_, err := PublishRelease(t.Context(), root, ReleasePublishOptions{
		Channel: "rc", ExpectedVersion: "v1.2.4-rc.42",
	}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN or GH_TOKEN") {
		t.Fatalf("missing credential error = %v", err)
	}
	repository, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Reference(plumbing.ReferenceName("refs/tags/v1.2.4-rc.42"), true); !errors.Is(err, plumbing.ErrReferenceNotFound) {
		t.Fatalf("RC tag created before credential check: %v", err)
	}
}

func TestExpectedReleaseGroupsUsesDeclaredNativeTargets(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Kind = ProjectKindTemplateRegistry
	manifest.TemplateRegistry.Templates[0].CI = PlatformDeclaration{
		CheckPlatforms: []string{"linux", "macos"}, ReleasePlatforms: []string{"linux", "macos"},
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	groups, err := ExpectedReleaseGroups(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(groups, ",") != "premise-rust-app-linux,premise-rust-app-macos" {
		t.Fatalf("native groups = %#v", groups)
	}
}

func TestPublishReleasePackagesRCRequiresMatchingTag(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	repository, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repository.Head()
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	useTestPackageManagers(t, testPackageManager{id: "cargo", publish: func(_ context.Context, _ string, version, task string, _, _ io.Writer) error {
		called++
		if version != "v1.2.4-rc.42" || task != "publish:rc" {
			t.Fatalf("wrong RC publication %s %s", version, task)
		}
		return nil
	}})
	if _, err := PublishReleasePackages(t.Context(), root, "rc", "v1.2.4-rc.42", io.Discard, io.Discard); err == nil {
		t.Fatal("RC packages published without matching tag")
	}
	if called != 0 {
		t.Fatalf("publisher called before tag check: %d", called)
	}
	if _, err := repository.CreateTag("v1.2.4-rc.42", head.Hash(), nil); err != nil {
		t.Fatal(err)
	}
	plan, err := PublishReleasePackages(t.Context(), root, "rc", "v1.2.4-rc.42", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v1.2.4-rc.42" || called != 1 {
		t.Fatalf("RC publication %#v, calls %d", plan, called)
	}
}

func TestPublishReleasePackagesRejectsWrongVersionBeforePublishing(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	called := false
	useTestPackageManagers(t, testPackageManager{id: "cargo", publish: func(context.Context, string, string, string, io.Writer, io.Writer) error {
		called = true
		return nil
	}})
	_, err := PublishReleasePackages(t.Context(), root, "stable", "v1.2.4", io.Discard, io.Discard)
	if err == nil || called {
		t.Fatalf("wrong stable version: err=%v published=%v", err, called)
	}
}

func TestStableTagAtIgnoresUnrelatedTags(t *testing.T) {
	repository, err := git.PlainInit(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(worktree.Filesystem.Root(), "README.md")
	if err := os.WriteFile(path, []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("feat: test", &git.CommitOptions{Author: &object.Signature{
		Name: "Test", Email: "test@example.com", When: time.Now(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateTag("pre-squash/feature/test", hash, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"v1.2.3", "v1.9.0", "v1.10.0", "v01.99.99"} {
		if _, err := repository.CreateTag(name, hash, nil); err != nil {
			t.Fatal(err)
		}
	}
	tag, err := stableTagAt(repository, hash)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.10.0" {
		t.Fatalf("stableTagAt() = %q, want v1.10.0", tag)
	}
}

func TestReleaseSubprocessCredentialBoundaries(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("premise")
	manifest.Workspace.PackageManagers = []string{"go"}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture")
	bin := t.TempDir()
	shim := `#!/bin/sh
set -eu
case "$*" in
  install)
    [ -z "${GITHUB_TOKEN:-}" ]
    [ -z "${GH_TOKEN:-}" ]
    [ -z "${CARGO_REGISTRY_TOKEN:-}" ]
    [ -z "${CARGO_TOKEN:-}" ]
    [ -z "${CRATES_TOKEN:-}" ]
    [ -z "${EXPECTED_INSTALL_DIRECTORY:-}" ] || [ "$(pwd -P)" = "$(cd "$EXPECTED_INSTALL_DIRECTORY" && pwd -P)" ]
    printf 'install\n' >> "$CAPTURE"
    ;;
  exec*)
    [ -z "${GITHUB_TOKEN:-}" ]
    [ -z "${GH_TOKEN:-}" ]
    [ -z "${CARGO_REGISTRY_TOKEN:-}" ]
    [ -z "${CARGO_TOKEN:-}" ]
    [ -z "${CRATES_TOKEN:-}" ]
    /usr/bin/env -0
    ;;
  *) exit 9 ;;
esac
`
	mise := filepath.Join(bin, "mise")
	if err := os.WriteFile(mise, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	goreleaserShim := `#!/bin/sh
set -eu
[ -z "${CARGO_REGISTRY_TOKEN:-}" ]
[ -z "${CARGO_TOKEN:-}" ]
[ -z "${CRATES_TOKEN:-}" ]
printf 'github:%s\n' "${GITHUB_TOKEN:-}" >> "$CAPTURE"
printf '%s\n' "$*" >> "$CAPTURE"
`
	if err := os.WriteFile(filepath.Join(bin, "goreleaser"), []byte(goreleaserShim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GITHUB_TOKEN", "github-secret")
	t.Setenv("GH_TOKEN", "gh-secret")
	t.Setenv("CARGO_REGISTRY_TOKEN", "cargo-secret")
	t.Setenv("CARGO_TOKEN", "raw-cargo-secret")
	t.Setenv("CRATES_TOKEN", "legacy-raw-cargo-secret")

	var output bytes.Buffer
	if err := os.WriteFile(capture, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	goPlugin := testPackageManager{id: "go", builds: "builds:\n  - id: premise\n"}
	useTestPackageManagers(t, goPlugin)
	if err := runGoReleaser(t.Context(), root, []PackageManagerBackend{goPlugin}, true, &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	text := string(captured)
	if !strings.Contains(text, "github:\nrelease --clean") || !strings.Contains(text, "--snapshot") {
		t.Fatalf("snapshot GoReleaser capture = %q", text)
	}
	if err := os.WriteFile(capture, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runGoReleaser(t.Context(), root, []PackageManagerBackend{goPlugin}, false, &output, &output); err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(published), "github:github-secret") || strings.Contains(string(published), "--snapshot") {
		t.Fatalf("publishing GoReleaser capture = %q", published)
	}
	fields := strings.Fields(text)
	foundConfig := false
	for index, field := range fields {
		if field == "-f" && index+1 < len(fields) {
			foundConfig = true
			file, err := os.Open(fields[index+1])
			if err == nil {
				if closeErr := file.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				t.Fatalf("temporary GoReleaser config still exists: %s", fields[index+1])
			}
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("inspect temporary GoReleaser config: %v", err)
			}
		}
	}
	if !foundConfig {
		t.Fatal("GoReleaser command omitted its temporary configuration")
	}

	cargoRoot := t.TempDir()
	cargoManifest := NewManifest("premise-cargo")
	cargoManifest.Workspace.PackageManagers = []string{"cargo"}
	cargoManifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{templateFixture("premise-rust-app", "app")}}
	if err := SaveManifest(filepath.Join(cargoRoot, ManifestFilename), cargoManifest); err != nil {
		t.Fatal(err)
	}
	cargoAppRoot := filepath.Join(cargoRoot, "templates", "premise-rust-app")
	if err := os.MkdirAll(cargoAppRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cargoAppRoot, "Cargo.toml"), []byte("[package]\nname = \"premise-rust-app\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cargoRoot, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(capture, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPECTED_INSTALL_DIRECTORY", cargoRoot)
	cargoPlugin := testPackageManager{
		id: "cargo", builds: "builds:\n  - id: premise-rust-app\n",
		workspace: func(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
			if err := (MiseTaskRunner{Stdout: stdout, Stderr: stderr}).Run(ctx, root, nil, "install"); err != nil {
				return "", false, err
			}
			return root, true, nil
		},
	}
	useTestPackageManagers(t, cargoPlugin)
	if err := runGoReleaser(t.Context(), cargoRoot, []PackageManagerBackend{cargoPlugin}, true, &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err = os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(captured); !strings.HasPrefix(text, "install\ngithub:\n") {
		t.Fatalf("Cargo GoReleaser capture = %q", text)
	}

}

func TestValidateReleaseFiles(t *testing.T) {
	makeGroup := func(t *testing.T, root, group, filename string) string {
		t.Helper()
		dir := filepath.Join(root, group)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, []byte("installer"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, test := range []struct {
		name      string
		setup     func(*testing.T, string)
		groups    []string
		wantError string
	}{
		{name: "ordinary only"},
		{name: "complete", groups: []string{"app-linux", "app-macos"}, setup: func(t *testing.T, dir string) {
			makeGroup(t, dir, "app-linux", "app.deb")
			makeGroup(t, dir, "app-macos", "app.dmg")
		}},
		{name: "missing group", groups: []string{"app-linux"}, wantError: "missing"},
		{name: "unexpected group", setup: func(t *testing.T, dir string) { makeGroup(t, dir, "unknown-linux", "other.deb") }, wantError: "unexpected"},
		{name: "duplicate basename", groups: []string{"app-linux", "app-macos"}, setup: func(t *testing.T, dir string) {
			makeGroup(t, dir, "app-linux", "same.zip")
			makeGroup(t, dir, "app-macos", "same.zip")
		}, wantError: "duplicate release asset"},
		{name: "empty file", groups: []string{"app-linux"}, setup: func(t *testing.T, dir string) {
			path := makeGroup(t, dir, "app-linux", "app.deb")
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, wantError: "nonempty regular"},
		{name: "symlink", groups: []string{"app-linux"}, setup: func(t *testing.T, dir string) {
			path := makeGroup(t, dir, "app-linux", "app.deb")
			if err := os.Symlink(path, filepath.Join(dir, "app-linux", "alias.deb")); err != nil {
				t.Fatal(err)
			}
		}, wantError: "nonempty regular"},
		{name: "nested files", groups: []string{"app-linux"}, setup: func(t *testing.T, dir string) {
			makeGroup(t, dir, "app-linux", "app.deb")
			if err := os.Mkdir(filepath.Join(dir, "app-linux", "nested"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, wantError: "nonempty regular"},
		{name: "unsafe group", groups: []string{"../app-linux"}, wantError: "invalid release file group"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.setup != nil {
				test.setup(t, root)
			}
			files, err := validateReleaseFiles(root, test.groups)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != len(test.groups) {
				t.Fatalf("files = %v, want %d", files, len(test.groups))
			}
		})
	}
}

func TestGoReleaserConfigWithFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(".git", "premise-release-files-123", "my installer.dmg")
	configuration, err := goReleaserConfigWithFiles(root, NewManifest("native-app"), nil, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	text := string(configuration)
	if !strings.Contains(text, "skip: true") || !strings.Contains(text, `extra_files:`) || !strings.Contains(text, `glob: ".git/premise-release-files-123/my installer.dmg"`) {
		t.Fatalf("file-only config = %q", text)
	}
	for _, invalid := range []string{filepath.Join(root, "installer.dmg"), "../installer.dmg"} {
		if _, err := goReleaserConfigWithFiles(root, NewManifest("native-app"), nil, []string{invalid}); err == nil {
			t.Fatalf("unsafe GoReleaser glob %q accepted", invalid)
		}
	}
}

func TestRunGoReleaserStagesExtraFiles(t *testing.T) {
	root := t.TempDir()
	if _, err := git.PlainInit(root, false); err != nil {
		t.Fatal(err)
	}
	manifest := NewManifest("native-app")
	manifest.Workspace.PackageManagers = []string{"go"}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	plugin := testPackageManager{id: "go"}
	useTestPackageManagers(t, plugin)
	source := filepath.Join(t.TempDir(), "installer.dmg")
	if err := os.WriteFile(source, []byte("native installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	mise := "#!/bin/sh\nif [ \"$1\" = exec ]; then /usr/bin/env -0; else exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(mise), 0o755); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "config.yml")
	goreleaser := "#!/bin/sh\ncp \"$4\" \"$CAPTURE\"\nexit 7\n"
	if err := os.WriteFile(filepath.Join(bin, "goreleaser"), []byte(goreleaser), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	var output bytes.Buffer
	runErr := runGoReleaserWithFiles(t.Context(), root, []PackageManagerBackend{plugin}, []string{source}, false, &output, &output)
	if runErr == nil {
		t.Fatal("expected GoReleaser shim failure")
	}
	config, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read GoReleaser configuration after %v: %v; output: %s", runErr, err, output.String())
	}
	if !strings.Contains(string(config), `glob: ".git/premise-release-files-`) || strings.Contains(string(config), source) {
		t.Fatalf("GoReleaser config uses a non-local extra file: %s", config)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "premise-release-files-") {
			t.Fatalf("staged files remain after GoReleaser failure: %s", entry.Name())
		}
	}
}

func TestStageGoReleaserFiles(t *testing.T) {
	root := t.TempDir()
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "installer.dmg")
	if err := os.WriteFile(source, []byte("native installer"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, cleanup, err := stageGoReleaserFiles(root, []string{source})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !filepath.IsLocal(files[0]) || !strings.HasPrefix(files[0], ".git"+string(filepath.Separator)) {
		t.Fatalf("staged GoReleaser files = %v", files)
	}
	staged := filepath.Join(root, files[0])
	contents, err := os.ReadFile(staged)
	if err != nil || string(contents) != "native installer" {
		t.Fatalf("staged file = %q, %v", contents, err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	status, err := worktree.Status()
	if err != nil || !status.IsClean() {
		t.Fatalf("release checkout became dirty: %v, %v", status, err)
	}
	cleanup()
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged file remains after cleanup: %v", err)
	}
	if _, _, err := stageGoReleaserFiles(root, []string{source, source}); err == nil {
		t.Fatal("duplicate staged asset name accepted")
	}
	if _, _, err := stageGoReleaserFiles(root, []string{"relative.dmg"}); err == nil {
		t.Fatal("relative source accepted")
	}
}
