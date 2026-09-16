package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

const goreleaserVersion = "2.18.1"

var stableVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

var (
	executeGoReleaser   = runGoReleaser
	executeCargoPublish = runCargoPublish
)

// ReleaseProfile identifies the artifacts a repository publishes.
type ReleaseProfile string

const (
	ReleaseProfileGo    ReleaseProfile = "go"
	ReleaseProfileCargo ReleaseProfile = "cargo"
)

// ReleasePlan describes the stable release decision for the current HEAD.
type ReleasePlan struct {
	Version  string
	ReuseTag bool
	Skip     bool
}

// DetectReleaseProfile returns the convention-driven release profile for root.
func DetectReleaseProfile(root string) (ReleaseProfile, error) {
	goModule, err := isRegularFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("inspect Go release convention: %w", err)
	}
	if goModule {
		return ReleaseProfileGo, nil
	}
	cargoWorkspace, err := isRegularFile(filepath.Join(root, "templates", "Cargo.toml"))
	if err != nil {
		return "", fmt.Errorf("inspect Cargo release convention: %w", err)
	}
	if cargoWorkspace {
		return ReleaseProfileCargo, nil
	}
	return "", fmt.Errorf("unsupported release repository: expected go.mod or templates/Cargo.toml at %s", root)
}

// PlanStableRelease fetches stable tags and decides whether HEAD should reuse a
// tag, create the next one, or skip because no release-worthy change exists.
func PlanStableRelease(ctx context.Context, root string) (ReleasePlan, error) {
	repository, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("open release repository: %w", err)
	}
	auth := releaseAuthentication()
	if err := fetchReleaseTags(ctx, repository, auth); err != nil {
		return ReleasePlan{}, fmt.Errorf("refresh release tags: %w", err)
	}

	head, err := repository.Head()
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("read release HEAD: %w", err)
	}
	if tag, err := stableTagAt(repository, head.Hash()); err != nil {
		return ReleasePlan{}, fmt.Errorf("find stable tag at HEAD: %w", err)
	} else if tag != "" {
		return ReleasePlan{Version: tag, ReuseTag: true}, nil
	}

	current, err := CurrentVersion(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("calculate current version; ensure the v0.0.0 bootstrap tag exists: %w", err)
	}
	next, err := NextVersion(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("calculate next version: %w", err)
	}
	if current == next {
		return ReleasePlan{Skip: true}, nil
	}
	return ReleasePlan{Version: next}, nil
}

// PrepareStableRelease plans the stable release and creates its tag when
// needed. It does not build or publish artifacts.
func PrepareStableRelease(ctx context.Context, root string, stdout io.Writer) (ReleasePlan, error) {
	plan, err := PlanStableRelease(ctx, root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("plan stable release: %w", err)
	}
	if plan.Skip {
		fmt.Fprintln(stdout, "No release-worthy change.")
		return plan, nil
	}
	if plan.ReuseTag {
		fmt.Fprintf(stdout, "Reusing existing stable tag at HEAD: %s\n", plan.Version)
		return plan, nil
	}
	if err := createAndPushReleaseTag(ctx, root, plan.Version, releaseAuthentication()); err != nil {
		return ReleasePlan{}, fmt.Errorf("prepare stable release: %w", err)
	}
	plan.ReuseTag = true
	fmt.Fprintf(stdout, "Created stable tag: %s\n", plan.Version)
	return plan, nil
}

// PublishGitHubRelease publishes artifacts for a stable tag already at HEAD.
// Package-registry credentials are removed from the GoReleaser environment.
func PublishGitHubRelease(ctx context.Context, root string, stdout, stderr io.Writer) (ReleasePlan, error) {
	plan, err := requirePreparedRelease(ctx, root, stdout)
	if err != nil || plan.Skip {
		return plan, err
	}
	profile, err := DetectReleaseProfile(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("detect release profile: %w", err)
	}
	if err := executeGoReleaser(ctx, root, profile, false, stdout, stderr); err != nil {
		return ReleasePlan{}, fmt.Errorf("publish GitHub release: %w", err)
	}
	return plan, nil
}

// PublishLanguagePackages publishes a Cargo registry's packages for a stable
// tag already at HEAD. GitHub credentials are removed from the task environment.
func PublishLanguagePackages(ctx context.Context, root string, stdout, stderr io.Writer) (ReleasePlan, error) {
	plan, err := requirePreparedRelease(ctx, root, stdout)
	if err != nil || plan.Skip {
		return plan, err
	}
	profile, err := DetectReleaseProfile(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("detect release profile: %w", err)
	}
	if profile != ReleaseProfileCargo {
		return ReleasePlan{}, fmt.Errorf("release profile %q does not publish language packages", profile)
	}
	if err := executeCargoPublish(ctx, root, plan.Version, stdout, stderr); err != nil {
		return ReleasePlan{}, fmt.Errorf("publish Cargo release: %w", err)
	}
	return plan, nil
}

// PublishStableRelease plans, tags, and publishes all configured outputs. CI
// can call the narrower functions in separate credential-bearing steps.
func PublishStableRelease(ctx context.Context, root string, stdout, stderr io.Writer) (ReleasePlan, error) {
	plan, err := PrepareStableRelease(ctx, root, stdout)
	if err != nil || plan.Skip {
		return plan, err
	}
	profile, err := DetectReleaseProfile(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("detect release profile: %w", err)
	}
	if err := executeGoReleaser(ctx, root, profile, false, stdout, stderr); err != nil {
		return ReleasePlan{}, fmt.Errorf("publish GitHub release: %w", err)
	}
	if profile == ReleaseProfileCargo {
		if err := executeCargoPublish(ctx, root, plan.Version, stdout, stderr); err != nil {
			return ReleasePlan{}, fmt.Errorf("publish Cargo release: %w", err)
		}
	}
	return plan, nil
}

func requirePreparedRelease(ctx context.Context, root string, stdout io.Writer) (ReleasePlan, error) {
	plan, err := PlanStableRelease(ctx, root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("plan stable release: %w", err)
	}
	if plan.Skip {
		fmt.Fprintln(stdout, "No release-worthy change.")
		return plan, nil
	}
	if !plan.ReuseTag {
		return ReleasePlan{}, errors.New("stable release tag is missing at HEAD; run `pm release prepare` first")
	}
	fmt.Fprintf(stdout, "Using stable tag at HEAD: %s\n", plan.Version)
	return plan, nil
}

// BuildReleaseSnapshot builds the complete release matrix without publishing a
// GitHub release, creating a tag, or publishing language packages.
func BuildReleaseSnapshot(ctx context.Context, root string, stdout, stderr io.Writer) error {
	profile, err := DetectReleaseProfile(root)
	if err != nil {
		return fmt.Errorf("detect release profile: %w", err)
	}
	if err := runGoReleaser(ctx, root, profile, true, stdout, stderr); err != nil {
		return fmt.Errorf("build release snapshot: %w", err)
	}
	return nil
}

// GoReleaserConfig generates Premise-owned GoReleaser configuration for a
// conventionally structured repository. Consumers do not need a config file.
func GoReleaserConfig(root string, profile ReleaseProfile) ([]byte, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, fmt.Errorf("load release manifest: %w", err)
	}
	project := manifest.Workspace.Name
	if project == "" {
		project = filepath.Base(filepath.Clean(root))
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "version: 2\n\nproject_name: %s\n\n", project)
	switch profile {
	case ReleaseProfileGo:
		fmt.Fprintf(&builder, `builds:
  - id: %s
    main: .
    binary: %s
    goos:`, project, project)
		builder.WriteString(`
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
    mod_timestamp: "{{ .CommitTimestamp }}"

archives:
  - formats:
      - tar.gz
    name_template: >-
      {{ .ProjectName }}-{{ .Tag }}-
      {{- if eq .Arch "amd64" }}x86_64{{- else }}aarch64{{- end }}-
      {{- if eq .Os "darwin" }}macos{{- else }}{{ .Os }}{{- end }}
`)
	case ReleaseProfileCargo:
		applications := make([]string, 0, len(manifest.Templates))
		for _, template := range manifest.Templates {
			if template.Kind == "app" {
				applications = append(applications, template.Name)
			}
		}
		if len(applications) == 0 {
			return nil, errors.New("cargo release profile requires at least one app template")
		}
		builder.WriteString("builds:\n")
		for _, application := range applications {
			fmt.Fprintf(&builder, `  - id: %s
    builder: rust
    binary: %s
    dir: .
    targets:
      - x86_64-unknown-linux-gnu
      - aarch64-unknown-linux-gnu
      - x86_64-apple-darwin
      - aarch64-apple-darwin
    flags:
      - --release
      - -p=%s

`, application, application, application)
		}
		builder.WriteString("archives:\n")
		for _, application := range applications {
			fmt.Fprintf(&builder, `  - id: %s
    ids:
      - %s
    formats:
      - tar.gz
    name_template: "{{ .Binary }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}"

`, application, application)
		}
	default:
		return nil, fmt.Errorf("unsupported release profile %q", profile)
	}
	builder.WriteString(`checksum:
  name_template: "checksums.txt"

changelog:
  sort: asc
  filters:
    exclude:
      - "^docs:"
      - "^test:"
      - "^ci:"
      - "^chore"

release:
  mode: keep-existing
  replace_existing_artifacts: true
`)
	return []byte(builder.String()), nil
}

func runGoReleaser(ctx context.Context, root string, profile ReleaseProfile, snapshot bool, stdout, stderr io.Writer) error {
	configuration, err := GoReleaserConfig(root, profile)
	if err != nil {
		return fmt.Errorf("generate GoReleaser configuration: %w", err)
	}
	temporary, err := os.CreateTemp("", "premise-goreleaser-*.yml")
	if err != nil {
		return fmt.Errorf("create temporary GoReleaser config: %w", err)
	}
	path := temporary.Name()
	defer os.Remove(path)
	if _, err := temporary.Write(configuration); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary GoReleaser config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary GoReleaser config: %w", err)
	}

	arguments := []string{"exec", "goreleaser@" + goreleaserVersion, "--", "goreleaser", "release", "--clean", "-f", path}
	if snapshot {
		arguments = append(arguments, "--snapshot")
	}
	if profile == ReleaseProfileCargo {
		arguments = append(arguments, "--parallelism", "1")
	}
	command := exec.CommandContext(ctx, "mise", arguments...)
	command.Dir = root
	if profile == ReleaseProfileCargo {
		command.Dir = filepath.Join(root, "templates")
	}
	command.Env = environmentWithout(os.Environ(), "CARGO_REGISTRY_TOKEN", "CRATES_TOKEN")
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run GoReleaser: %w", err)
	}
	return nil
}

func runCargoPublish(ctx context.Context, root, version string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, "mise", "run", "publish")
	command.Dir = root
	command.Env = append(
		environmentWithout(os.Environ(), "GITHUB_TOKEN", "GH_TOKEN", "CRATES_TOKEN"),
		"RELEASE_VERSION="+version,
	)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("publish Cargo packages: %w", err)
	}
	return nil
}

func createAndPushReleaseTag(ctx context.Context, root, version string, auth transport.AuthMethod) (resultErr error) {
	if !stableVersionPattern.MatchString(version) {
		return fmt.Errorf("release version %q is not a stable vMAJOR.MINOR.PATCH tag", version)
	}
	if auth == nil {
		return errors.New("GITHUB_TOKEN or GH_TOKEN is required to push a stable release tag")
	}
	repository, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return fmt.Errorf("open release repository: %w", err)
	}
	head, err := repository.Head()
	if err != nil {
		return fmt.Errorf("read release HEAD: %w", err)
	}
	if _, err := repository.CreateTag(version, head.Hash(), nil); err != nil {
		return fmt.Errorf("create stable tag %s: %w", version, err)
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if err := repository.DeleteTag(version); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove unpushed local tag %s: %w", version, err))
		}
	}()

	remote, err := repository.Remote("origin")
	if err != nil {
		return fmt.Errorf("open origin remote: %w", err)
	}
	refspec := gitconfig.RefSpec("refs/tags/" + version + ":refs/tags/" + version)
	if err := remote.PushContext(ctx, &git.PushOptions{Auth: auth, RefSpecs: []gitconfig.RefSpec{refspec}}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("push stable tag %s: %w", version, err)
	}
	return nil
}

func fetchReleaseTags(ctx context.Context, repository *git.Repository, auth transport.AuthMethod) error {
	remote, err := repository.Remote("origin")
	if errors.Is(err, git.ErrRemoteNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open origin remote: %w", err)
	}
	err = remote.FetchContext(ctx, &git.FetchOptions{
		Auth:     auth,
		Force:    true,
		Tags:     git.AllTags,
		RefSpecs: []gitconfig.RefSpec{"+refs/tags/*:refs/tags/*"},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetch release tags: %w", err)
	}
	return nil
}

func stableTagAt(repository *git.Repository, hash plumbing.Hash) (string, error) {
	var matches []string
	tags, err := repository.Tags()
	if err != nil {
		return "", fmt.Errorf("list release tags: %w", err)
	}
	err = tags.ForEach(func(reference *plumbing.Reference) error {
		name := reference.Name().Short()
		if !stableVersionPattern.MatchString(name) {
			return nil
		}
		target := reference.Hash()
		if annotated, err := repository.TagObject(target); err == nil {
			target = annotated.Target
		}
		if target == hash {
			matches = append(matches, name)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inspect release tags: %w", err)
	}
	if len(matches) == 0 {
		return "", nil
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

func releaseAuthentication() transport.AuthMethod {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: token}
}

func environmentWithout(environment []string, names ...string) []string {
	removed := make(map[string]struct{}, len(names))
	for _, name := range names {
		removed[name] = struct{}{}
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name := entry
		if separator := strings.IndexByte(entry, '='); separator >= 0 {
			name = entry[:separator]
		}
		if _, ok := removed[name]; !ok {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func isRegularFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return info.Mode().IsRegular(), nil
}
