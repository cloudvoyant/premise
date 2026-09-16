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
)

func TestDetectReleaseProfile(t *testing.T) {
	goRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cargoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cargoRoot, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cargoRoot, "templates", "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		root string
		want ReleaseProfile
	}{
		{root: goRoot, want: ReleaseProfileGo},
		{root: cargoRoot, want: ReleaseProfileCargo},
	} {
		got, err := DetectReleaseProfile(test.root)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("DetectReleaseProfile(%s) = %q, want %q", test.root, got, test.want)
		}
	}
}

func TestGoReleaserConfigIsConventionDriven(t *testing.T) {
	goRoot := t.TempDir()
	goManifest := NewManifest("premise")
	if err := SaveManifest(filepath.Join(goRoot, ManifestFilename), goManifest); err != nil {
		t.Fatal(err)
	}
	goConfig, err := GoReleaserConfig(goRoot, ReleaseProfileGo)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"project_name: premise",
		"binary: premise",
		"{{ .ProjectName }}-{{ .Tag }}-",
		"mode: keep-existing",
		"replace_existing_artifacts: true",
	} {
		if !strings.Contains(string(goConfig), want) {
			t.Errorf("Go config does not contain %q", want)
		}
	}

	cargoRoot := t.TempDir()
	cargoManifest := NewManifest("premise-cargo")
	cargoManifest.Templates = []Template{
		templateFixture("premise-rust-lib", "lib"),
		templateFixture("premise-rust-app", "app"),
		templateFixture("premise-clap-cli", "app"),
		templateFixture("premise-ratatui-app", "app"),
	}
	if err := SaveManifest(filepath.Join(cargoRoot, ManifestFilename), cargoManifest); err != nil {
		t.Fatal(err)
	}
	cargoConfig, err := GoReleaserConfig(cargoRoot, ReleaseProfileCargo)
	if err != nil {
		t.Fatal(err)
	}
	text := string(cargoConfig)
	for _, want := range []string{
		"project_name: premise-cargo",
		"id: premise-rust-app",
		"id: premise-clap-cli",
		"id: premise-ratatui-app",
		"builder: rust",
		"x86_64-unknown-linux-gnu",
		"aarch64-unknown-linux-gnu",
		"x86_64-apple-darwin",
		"aarch64-apple-darwin",
		`name_template: "{{ .Binary }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Cargo config does not contain %q", want)
		}
	}
	if strings.Contains(text, "premise-rust-lib") {
		t.Error("Cargo library unexpectedly included in binary release config")
	}
	if count := strings.Count(text, "builder: rust"); count != 3 {
		t.Fatalf("Cargo config contains %d Rust builds, want 3", count)
	}
	if count := strings.Count(text, "    targets:"); count != 3 {
		t.Fatalf("Cargo config contains %d target matrices, want 3", count)
	}
	if count := strings.Count(text, "    name_template:"); count != 3 {
		t.Fatalf("Cargo config contains %d archive templates, want 3", count)
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
	originalGoReleaser := executeGoReleaser
	originalCargoPublish := executeCargoPublish
	defer func() {
		executeGoReleaser = originalGoReleaser
		executeCargoPublish = originalCargoPublish
	}()

	var calls []string
	executeGoReleaser = func(_ context.Context, _ string, profile ReleaseProfile, snapshot bool, _, _ io.Writer) error {
		if profile != ReleaseProfileCargo || snapshot {
			t.Fatalf("unexpected GoReleaser arguments: profile=%q snapshot=%v", profile, snapshot)
		}
		calls = append(calls, "github")
		return nil
	}
	executeCargoPublish = func(_ context.Context, _ string, version string, _, _ io.Writer) error {
		if version != "v1.2.3" {
			t.Fatalf("Cargo version = %q, want v1.2.3", version)
		}
		calls = append(calls, "cargo")
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
	executeGoReleaser = func(_ context.Context, _ string, _ ReleaseProfile, _ bool, _, _ io.Writer) error {
		calls = append(calls, "github")
		return publishErr
	}
	executeCargoPublish = func(_ context.Context, _ string, _ string, _, _ io.Writer) error {
		calls = append(calls, "cargo")
		return nil
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
	manifest.Templates = []Template{templateFixture("premise-rust-app", "app")}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
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
	if _, err := repository.CreateTag("v1.2.3", hash, nil); err != nil {
		t.Fatal(err)
	}
	tag, err := stableTagAt(repository, hash)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v1.2.3" {
		t.Fatalf("stableTagAt() = %q, want v1.2.3", tag)
	}
}

func TestReleaseSubprocessCredentialBoundaries(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("premise")
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture")
	bin := t.TempDir()
	shim := `#!/bin/sh
set -eu
case "$*" in
  exec*)
    [ -z "${CARGO_REGISTRY_TOKEN:-}" ]
    [ -z "${CRATES_TOKEN:-}" ]
    printf 'github:%s\n' "${GITHUB_TOKEN:-}" > "$CAPTURE"
    printf '%s\n' "$*" >> "$CAPTURE"
    ;;
  "run publish")
    [ -z "${GITHUB_TOKEN:-}" ]
    [ -z "${GH_TOKEN:-}" ]
    [ -z "${CRATES_TOKEN:-}" ]
    printf 'cargo:%s\nversion:%s\n' "${CARGO_REGISTRY_TOKEN:-}" "${RELEASE_VERSION:-}" > "$CAPTURE"
    ;;
  *) exit 9 ;;
esac
`
	mise := filepath.Join(bin, "mise")
	if err := os.WriteFile(mise, []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GITHUB_TOKEN", "github-secret")
	t.Setenv("GH_TOKEN", "gh-secret")
	t.Setenv("CARGO_REGISTRY_TOKEN", "cargo-secret")
	t.Setenv("CRATES_TOKEN", "raw-cargo-secret")

	var output bytes.Buffer
	if err := runGoReleaser(t.Context(), root, ReleaseProfileGo, true, &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	text := string(captured)
	if !strings.Contains(text, "github:github-secret") || !strings.Contains(text, "goreleaser@"+goreleaserVersion) {
		t.Fatalf("GoReleaser capture = %q", text)
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

	if err := runCargoPublish(t.Context(), root, "v1.2.3", &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err = os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(captured), "cargo:cargo-secret\nversion:v1.2.3\n"; got != want {
		t.Fatalf("Cargo capture = %q, want %q", got, want)
	}
}

func TestEnvironmentWithoutSecrets(t *testing.T) {
	environment := []string{"PATH=/bin", "GITHUB_TOKEN=github", "CARGO_REGISTRY_TOKEN=cargo", "VALUE=a=b"}
	filtered := environmentWithout(environment, "GITHUB_TOKEN", "CARGO_REGISTRY_TOKEN")
	if got := strings.Join(filtered, "\n"); got != "PATH=/bin\nVALUE=a=b" {
		t.Fatalf("environmentWithout() = %q", got)
	}
}
