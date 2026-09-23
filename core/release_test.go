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
	if err := os.WriteFile(filepath.Join(cargoRoot, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
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
	cargoManifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{
		templateFixture("premise-rust-lib", "lib"),
		templateFixture("premise-rust-app", "app"),
		templateFixture("premise-clap-cli", "app"),
		templateFixture("premise-ratatui-app", "app"),
		templateFixture("premise-tauri-app", "app"),
	}}
	if err := SaveManifest(filepath.Join(cargoRoot, ManifestFilename), cargoManifest); err != nil {
		t.Fatal(err)
	}
	for name, publish := range map[string]bool{
		"premise-rust-lib":    true,
		"premise-rust-app":    true,
		"premise-clap-cli":    false,
		"premise-ratatui-app": true,
	} {
		directory := filepath.Join(cargoRoot, "templates", name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "[package]\nname = \"" + name + "\"\nversion = \"0.1.0\"\n"
		if !publish {
			content += "publish = false\n"
		}
		if err := os.WriteFile(filepath.Join(directory, "Cargo.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nestedDirectory := filepath.Join(cargoRoot, "templates", "premise-tauri-app", "src-tauri")
	if err := os.MkdirAll(nestedDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDirectory, "Cargo.toml"), []byte("[package]\nname = \"premise-tauri-app\"\nversion = \"0.1.0\"\npublish = false\n"), 0o644); err != nil {
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
	for _, unwanted := range []string{"premise-rust-lib", "premise-tauri-app"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("Cargo config unexpectedly contains %q", unwanted)
		}
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

func TestGoReleaserConfigRequiresDirectCargoApp(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("premise-cargo")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{
		templateFixture("premise-rust-lib", "lib"),
		templateFixture("premise-tauri-app", "app"),
	}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	libraryRoot := filepath.Join(root, "templates", "premise-rust-lib")
	nestedRoot := filepath.Join(root, "templates", "premise-tauri-app", "src-tauri")
	for _, directory := range []string{libraryRoot, nestedRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(libraryRoot, "Cargo.toml"), []byte("[package]\nname = \"premise-rust-lib\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedRoot, "Cargo.toml"), []byte("[package]\nname = \"premise-tauri-app\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := GoReleaserConfig(root, ReleaseProfileCargo)
	if err == nil || err.Error() != "cargo release profile requires at least one app template" {
		t.Fatalf("GoReleaserConfig() error = %v", err)
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
	if err := runGoReleaser(t.Context(), root, ReleaseProfileGo, true, &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	text := string(captured)
	if !strings.Contains(text, "github:github-secret") || !strings.Contains(text, "release --clean") {
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

	cargoRoot := t.TempDir()
	cargoManifest := NewManifest("premise-cargo")
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
	if err := runGoReleaser(t.Context(), cargoRoot, ReleaseProfileCargo, true, &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err = os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(captured); !strings.HasPrefix(text, "install\ngithub:github-secret\n") {
		t.Fatalf("Cargo GoReleaser capture = %q", text)
	}

}
