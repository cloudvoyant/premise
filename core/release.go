package core

// Release module responsibilities:
//   - plan and prepare strict stable repository tags;
//   - coordinate release candidates and stable publication;
//   - generate temporary GoReleaser policy;
//   - preserve credential boundaries between GitHub and package registries.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
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
	cargoWorkspace, err := isCargoRegistry(root)
	if err != nil {
		return "", fmt.Errorf("inspect Cargo release convention: %w", err)
	}
	if cargoWorkspace {
		return ReleaseProfileCargo, nil
	}
	return "", fmt.Errorf("unsupported release repository: expected go.mod, Cargo.toml, or templates/Cargo.toml at %s", root)
}

func publishReleaseCandidate(ctx context.Context, root string, kind ProjectKind, stdout, stderr io.Writer) error {
	cargoRegistry, err := isCargoRegistry(root)
	if err != nil {
		return fmt.Errorf("inspect Cargo RC convention: %w", err)
	}
	if cargoRegistry {
		identifier := os.Getenv("GITHUB_RUN_NUMBER")
		if err := ValidateRCIdentifier(identifier); err != nil {
			return fmt.Errorf("resolve Cargo RC identifier: %w", err)
		}
		version, err := ReleaseCandidateVersion(root, identifier)
		if err != nil {
			return err
		}
		return publishCargoPackages(ctx, root, version, "publish:rc", stdout, stderr)
	}

	if kind == "" {
		kind, err = DetectProjectKind(root)
		if err != nil {
			return fmt.Errorf("detect RC project kind: %w", err)
		}
	}
	mise := miseRunner{
		Stdout:  stdout,
		Stderr:  stderr,
		Ceiling: filepath.Dir(filepath.Clean(root)),
	}
	exists, err := mise.taskExists(ctx, root, "publish:rc")
	if err != nil {
		return fmt.Errorf("inspect publish:rc task: %w", err)
	}
	if exists {
		if err := mise.run(ctx, root, nil, "run", "publish:rc"); err != nil {
			return fmt.Errorf("publish RC: %w", err)
		}
		return nil
	}
	if kind != ProjectKindMonorepo {
		return errors.New("marked RC push requires a root publish:rc task")
	}
	if err := mise.run(ctx, root, nil, "run", "--jobs", "1", "//...:publish:rc"); err != nil {
		return fmt.Errorf("publish monorepo RC: %w", err)
	}
	return nil
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
		templates := manifest.DeclaredTemplates()
		applications := make([]string, 0, len(templates))
		for _, template := range templates {
			if template.Kind != "app" {
				continue
			}
			pkg, found, err := inspectCargoTemplatePackage(root, template)
			if err != nil {
				return nil, err
			}
			if found {
				applications = append(applications, pkg.Name)
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
	workingDirectory := root
	if profile == ReleaseProfileCargo {
		workspaceDirectory, found, err := cargoWorkspaceDirectory(root)
		if err != nil {
			return fmt.Errorf("inspect Cargo release workspace: %w", err)
		}
		if !found {
			return errors.New("Cargo release workspace is missing Cargo.toml")
		}
		workingDirectory = workspaceDirectory
		if err := installReleaseTools(ctx, workingDirectory, stdout, stderr); err != nil {
			return fmt.Errorf("prepare Cargo release toolchain: %w", err)
		}
	}

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

	arguments := []string{"release", "--clean", "-f", path}
	if snapshot {
		arguments = append(arguments, "--snapshot")
	}
	if profile == ReleaseProfileCargo {
		arguments = append(arguments, "--parallelism", "1")
	}
	mise := miseRunner{Stderr: stderr}
	toolEnvironment, err := mise.toolEnvironment(ctx, workingDirectory, "goreleaser@"+goreleaserVersion)
	if err != nil {
		return fmt.Errorf("resolve GoReleaser environment: %w", err)
	}
	goreleaser, err := executableFromEnvironment("goreleaser", toolEnvironment)
	if err != nil {
		return err
	}
	releaseEnvironment := withoutEnvironment(toolEnvironment, publicationCredentialEnvironment...)
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if value, ok := os.LookupEnv(name); ok {
			releaseEnvironment = append(releaseEnvironment, name+"="+value)
		}
	}
	command := exec.CommandContext(ctx, goreleaser, arguments...)
	command.Dir = workingDirectory
	command.Env = releaseEnvironment
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run GoReleaser: %w", err)
	}
	return nil
}

func installReleaseTools(ctx context.Context, workingDirectory string, stdout, stderr io.Writer) error {
	mise := miseRunner{Stdout: stdout, Stderr: stderr}
	if err := mise.run(ctx, workingDirectory, nil, "install"); err != nil {
		return fmt.Errorf("install release tools: %w", err)
	}
	return nil
}

func executableFromEnvironment(name string, environment []string) (string, error) {
	var pathValue string
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == "PATH" {
			pathValue = value
			break
		}
	}
	for _, directory := range filepath.SplitList(pathValue) {
		candidate := filepath.Join(directory, name)
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("resolve %s executable from Mise tool environment", name)
}

func runCargoPublish(ctx context.Context, root, version string, stdout, stderr io.Writer) error {
	return publishCargoPackages(ctx, root, version, "publish", stdout, stderr)
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
	selected := ""
	var selectedVersion *semver.Version
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
		if target != hash {
			return nil
		}
		candidate, err := semver.StrictNewVersion(strings.TrimPrefix(name, "v"))
		if err != nil {
			return nil
		}
		if selectedVersion == nil || candidate.GreaterThan(selectedVersion) {
			selected = name
			selectedVersion = candidate
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inspect release tags: %w", err)
	}
	return selected, nil
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
