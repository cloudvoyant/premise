package core

// Release module responsibilities:
//   - plan and prepare strict stable repository tags;
//   - coordinate release candidates and stable publication;
//   - generate temporary GoReleaser policy;
//   - preserve credential boundaries between GitHub and package registries.

import (
	"context"
	"encoding/json"
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
var releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?$`)

var executeGoReleaser = runGoReleaser
var pushReleaseTag = createAndPushReleaseTag

// ReleasePlan describes the stable release decision for the current HEAD.
type ReleasePlan struct {
	Version  string
	ReuseTag bool
	Skip     bool
}

// ReleasePublishOptions describes the narrow convergence publication step.
// Validation and tag creation happen before GoReleaser is invoked.
type ReleasePublishOptions struct {
	Channel          string
	ExpectedVersion  string
	TriggeringCommit string
	FilesDir         string
	ExpectedGroups   []string
}

// PublishRelease validates staged files, creates or reuses the matching tag,
// and invokes GoReleaser once with ordinary and staged assets.
func PublishRelease(ctx context.Context, root string, options ReleasePublishOptions, stdout, stderr io.Writer) (ReleasePlan, error) {
	if options.Channel != "stable" && options.Channel != "rc" {
		return ReleasePlan{}, fmt.Errorf("invalid release channel %q", options.Channel)
	}
	if options.ExpectedVersion == "" {
		return ReleasePlan{}, errors.New("expected release version is required")
	}
	if !releaseVersionPattern.MatchString(options.ExpectedVersion) ||
		(options.Channel == "stable" && !stableVersionPattern.MatchString(options.ExpectedVersion)) ||
		(options.Channel == "rc" && stableVersionPattern.MatchString(options.ExpectedVersion)) {
		return ReleasePlan{}, fmt.Errorf("invalid %s expected version %q", options.Channel, options.ExpectedVersion)
	}
	if goreleaserToken() == "" {
		return ReleasePlan{}, errors.New("GITHUB_TOKEN or GH_TOKEN is required before creating a release tag")
	}
	// RC publication must never consult stable version planning: an RC tag is
	// intentionally not a stable release candidate for PlanStableRelease.
	var plan ReleasePlan
	var err error
	if options.Channel == "rc" {
		plan = ReleasePlan{Version: options.ExpectedVersion}
	} else {
		plan, err = PlanStableRelease(ctx, root)
		if err != nil {
			return ReleasePlan{}, err
		}
	}
	files, err := validateReleaseFiles(options.FilesDir, options.ExpectedGroups)
	if err != nil {
		return ReleasePlan{}, err
	}
	repository, openErr := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
	if openErr != nil {
		return ReleasePlan{}, openErr
	}
	head, headErr := repository.Head()
	if headErr != nil {
		return ReleasePlan{}, headErr
	}
	if options.Channel == "rc" {
		if err := fetchReleaseTags(ctx, repository, releaseAuthentication()); err != nil {
			return ReleasePlan{}, fmt.Errorf("refresh RC tags: %w", err)
		}
		tagHash, tagErr := repository.Tags()
		if tagErr != nil {
			return ReleasePlan{}, tagErr
		}
		conflict := false
		if err := tagHash.ForEach(func(ref *plumbing.Reference) error {
			if ref.Name().Short() != options.ExpectedVersion {
				return nil
			}
			target := ref.Hash()
			if annotated, e := repository.TagObject(target); e == nil {
				target = annotated.Target
			}
			if target == head.Hash() {
				plan.ReuseTag = true
			} else {
				conflict = true
			}
			return nil
		}); err != nil {
			return ReleasePlan{}, fmt.Errorf("inspect RC tags: %w", err)
		}
		if conflict {
			return ReleasePlan{}, fmt.Errorf("release tag %s exists away from HEAD", options.ExpectedVersion)
		}
	}
	if options.TriggeringCommit != "" {
		repository, openErr := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
		if openErr != nil {
			return ReleasePlan{}, openErr
		}
		head, headErr := repository.Head()
		if headErr != nil || !strings.EqualFold(head.Hash().String(), options.TriggeringCommit) {
			return ReleasePlan{}, fmt.Errorf("release HEAD does not match triggering commit %q", options.TriggeringCommit)
		}
	}
	if plan.Skip || plan.Version != options.ExpectedVersion {
		return ReleasePlan{}, fmt.Errorf("expected release %s does not match planned version %s", options.ExpectedVersion, plan.Version)
	}
	if !plan.ReuseTag {
		if err := pushReleaseTag(ctx, root, plan.Version, releaseAuthentication()); err != nil {
			return ReleasePlan{}, fmt.Errorf("create release tag: %w", err)
		}
		plan.ReuseTag = true
	}
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return ReleasePlan{}, err
	}
	if err := runGoReleaserWithFiles(ctx, root, plugins, files, false, stdout, stderr); err != nil {
		return ReleasePlan{}, fmt.Errorf("publish GitHub release: %w", err)
	}
	return plan, nil
}

func publishReleaseCandidate(ctx context.Context, root string, kind ProjectKind, stdout, stderr io.Writer) error {
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return err
	}
	packagePublishers := make([]PackageManagerBackend, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin.SupportsPackages() {
			packagePublishers = append(packagePublishers, plugin)
		}
	}
	if len(packagePublishers) != 0 {
		identifier := os.Getenv("GITHUB_RUN_NUMBER")
		if err := ValidateRCIdentifier(identifier); err != nil {
			return fmt.Errorf("resolve RC identifier: %w", err)
		}
		version, err := ReleaseCandidateVersion(root, identifier)
		if err != nil {
			return err
		}
		for _, plugin := range packagePublishers {
			if err := plugin.PublishPackages(ctx, root, version, "publish:rc", stdout, stderr); err != nil {
				return fmt.Errorf("publish %s RC packages: %w", plugin.ID(), err)
			}
		}
		return nil
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
		return ReleasePlan{}, fmt.Errorf("calculate current version: %w", err)
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
	return prepareStableRelease(ctx, root, stdout, false)
}

func prepareStableRelease(ctx context.Context, root string, stdout io.Writer, fullPublish bool) (ReleasePlan, error) {
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
	if fullPublish {
		if err := preflightStablePublication(ctx, root, plan.Version); err != nil {
			return ReleasePlan{}, fmt.Errorf("preflight stable publication: %w", err)
		}
	}
	if err := pushReleaseTag(ctx, root, plan.Version, releaseAuthentication()); err != nil {
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
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("resolve package managers: %w", err)
	}
	if err := executeGoReleaser(ctx, root, plugins, false, stdout, stderr); err != nil {
		return ReleasePlan{}, fmt.Errorf("publish GitHub release: %w", err)
	}
	return plan, nil
}

// PublishLanguagePackages publishes eligible registry packages for a stable
// tag already at HEAD. GitHub credentials are removed from the task environment.
func PublishLanguagePackages(ctx context.Context, root string, stdout, stderr io.Writer) (ReleasePlan, error) {
	plan, err := requirePreparedRelease(ctx, root, stdout)
	if err != nil || plan.Skip {
		return plan, err
	}
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("resolve package managers: %w", err)
	}
	published := false
	for _, plugin := range plugins {
		if !plugin.SupportsPackages() {
			continue
		}
		published = true
		if err := plugin.PublishPackages(ctx, root, plan.Version, "publish", stdout, stderr); err != nil {
			return ReleasePlan{}, fmt.Errorf("publish %s packages: %w", plugin.ID(), err)
		}
	}
	if !published {
		return ReleasePlan{}, errors.New("workspace package managers do not publish language packages")
	}
	return plan, nil
}

// PublishReleasePackages checks the exact tag at HEAD before invoking any
// registry publisher. The workflow runs this only after GitHub publication.
func PublishReleasePackages(ctx context.Context, root, channel, expectedVersion string, stdout, stderr io.Writer) (ReleasePlan, error) {
	if (channel != "stable" && channel != "rc") || !releaseVersionPattern.MatchString(expectedVersion) ||
		(channel == "stable" && !stableVersionPattern.MatchString(expectedVersion)) ||
		(channel == "rc" && stableVersionPattern.MatchString(expectedVersion)) {
		return ReleasePlan{}, fmt.Errorf("invalid %s package release version %q", channel, expectedVersion)
	}
	var plan ReleasePlan
	if channel == "stable" {
		var err error
		plan, err = requirePreparedRelease(ctx, root, stdout)
		if err != nil {
			return ReleasePlan{}, err
		}
		if plan.Skip || plan.Version != expectedVersion {
			return ReleasePlan{}, fmt.Errorf("expected stable release %s does not match planned version %s", expectedVersion, plan.Version)
		}
	} else {
		repository, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
		if err != nil {
			return ReleasePlan{}, err
		}
		if err := fetchReleaseTags(ctx, repository, releaseAuthentication()); err != nil {
			return ReleasePlan{}, fmt.Errorf("refresh RC tags: %w", err)
		}
		head, err := repository.Head()
		if err != nil {
			return ReleasePlan{}, err
		}
		ref, err := repository.Tag(expectedVersion)
		if err != nil {
			return ReleasePlan{}, fmt.Errorf("expected RC tag %s is missing: %w", expectedVersion, err)
		}
		target := ref.Hash()
		if annotated, err := repository.TagObject(target); err == nil {
			target = annotated.Target
		}
		if target != head.Hash() {
			return ReleasePlan{}, fmt.Errorf("expected RC tag %s is not at HEAD", expectedVersion)
		}
		plan = ReleasePlan{Version: expectedVersion, ReuseTag: true}
	}
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("resolve package managers: %w", err)
	}
	published := false
	task := "publish"
	if channel == "rc" {
		task = "publish:rc"
	}
	for _, plugin := range plugins {
		if !plugin.SupportsPackages() {
			continue
		}
		published = true
		if err := plugin.PublishPackages(ctx, root, plan.Version, task, stdout, stderr); err != nil {
			return ReleasePlan{}, fmt.Errorf("publish %s packages: %w", plugin.ID(), err)
		}
	}
	if !published {
		return ReleasePlan{}, errors.New("workspace package managers do not publish language packages")
	}
	return plan, nil
}

// PublishStableRelease plans, tags, and publishes all configured outputs. CI
// can call the narrower functions in separate credential-bearing steps.
func PublishStableRelease(ctx context.Context, root string, stdout, stderr io.Writer) (ReleasePlan, error) {
	planned, err := PlanStableRelease(ctx, root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("plan stable release: %w", err)
	}
	if !planned.Skip && !planned.ReuseTag {
		_, plugins, err := loadReleaseBackends(root)
		if err != nil {
			return ReleasePlan{}, fmt.Errorf("resolve package managers: %w", err)
		}
		if err := executeGoReleaser(ctx, root, plugins, true, stdout, stderr); err != nil {
			return ReleasePlan{}, fmt.Errorf("preflight release artifacts before tagging: %w", err)
		}
	}
	plan, err := prepareStableRelease(ctx, root, stdout, true)
	if err != nil || plan.Skip {
		return plan, err
	}
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return ReleasePlan{}, fmt.Errorf("resolve package managers: %w", err)
	}
	if err := executeGoReleaser(ctx, root, plugins, false, stdout, stderr); err != nil {
		return ReleasePlan{}, fmt.Errorf("publish GitHub release: %w", err)
	}
	for _, plugin := range plugins {
		if !plugin.SupportsPackages() {
			continue
		}
		if err := plugin.PublishPackages(ctx, root, plan.Version, "publish", stdout, stderr); err != nil {
			return ReleasePlan{}, fmt.Errorf("publish %s packages: %w", plugin.ID(), err)
		}
	}
	return plan, nil
}

func preflightStablePublication(ctx context.Context, root, version string) error {
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return fmt.Errorf("resolve package managers: %w", err)
	}
	return preflightStablePublicationPlugins(ctx, root, version, plugins)
}

func preflightStablePublicationPlugins(ctx context.Context, root, version string, plugins []PackageManagerBackend) error {
	var failures []error
	for _, plugin := range plugins {
		if !plugin.SupportsPackages() {
			continue
		}
		if err := plugin.PreflightPublication(ctx, root, version, "publish"); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", plugin.ID(), err))
		}
	}
	return errors.Join(failures...)
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

// BuildReleaseArtifacts builds the complete release matrix without publishing
// a release, creating a tag, or publishing language packages.
func BuildReleaseArtifacts(ctx context.Context, root string, stdout, stderr io.Writer) error {
	_, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return fmt.Errorf("resolve package managers: %w", err)
	}
	if err := executeGoReleaser(ctx, root, plugins, true, stdout, stderr); err != nil {
		return fmt.Errorf("build release artifacts: %w", err)
	}
	return nil
}

// GoReleaserConfig generates Premise-owned GoReleaser configuration for a
// conventionally structured repository. Consumers do not need a config file.
func GoReleaserConfig(root string) ([]byte, error) {
	manifest, plugins, err := loadReleaseBackends(root)
	if err != nil {
		return nil, err
	}
	return goReleaserConfig(root, manifest, plugins)
}

func goReleaserConfig(root string, manifest Config, plugins []PackageManagerBackend) ([]byte, error) {
	return goReleaserConfigWithFiles(root, manifest, plugins, nil)
}

func goReleaserConfigWithFiles(root string, manifest Config, plugins []PackageManagerBackend, files []string) ([]byte, error) {
	project := manifest.Workspace.Name
	if project == "" {
		project = filepath.Base(filepath.Clean(root))
	}

	builds, err := packageManagerReleaseConfig(root, manifest, plugins)
	if err != nil || (builds == "" && len(files) == 0) {
		return nil, err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "version: 2\n\nproject_name: %s\n\n", project)
	if builds == "" {
		// Explicitly disable GoReleaser's implicit default Go build for file-only releases.
		builder.WriteString("builds:\n  - skip: true\n\n")
	} else {
		builder.WriteString(builds)
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
	for _, file := range files {
		if !filepath.IsAbs(file) {
			return nil, fmt.Errorf("release file %q must be absolute", file)
		}
	}
	if len(files) != 0 {
		builder.WriteString("  extra_files:\n")
		for _, file := range files {
			quoted, err := json.Marshal(file)
			if err != nil {
				return nil, fmt.Errorf("encode release file %q: %w", file, err)
			}
			fmt.Fprintf(&builder, "    - glob: %s\n", quoted)
		}
	}
	return []byte(builder.String()), nil
}

// ExpectedReleaseGroups derives native artifact groups from the manifest, not
// from the downloaded tree. Unexpected uploaded groups must fail closed.
func ExpectedReleaseGroups(root string) ([]string, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	projects, err := SelectCIProjects(root, manifest, CIFlowOnCommit)
	if err != nil {
		return nil, err
	}
	groups := []string{}
	for _, project := range projects {
		for _, platform := range project.ReleasePlatforms {
			groups = append(groups, project.Name+"-"+platform)
		}
	}
	return groups, nil
}

// validateReleaseFiles checks the entire downloaded artifact tree before any
// tag is created. Each expected group is a flat project-platform directory.
func validateReleaseFiles(filesDir string, groups []string) ([]string, error) {
	if filesDir == "" && len(groups) == 0 {
		return nil, nil
	}
	if !filepath.IsAbs(filesDir) {
		return nil, errors.New("release files-dir must be absolute")
	}
	root, err := os.Lstat(filesDir)
	if err != nil {
		return nil, fmt.Errorf("inspect release files-dir: %w", err)
	}
	if !root.IsDir() {
		return nil, errors.New("release files-dir must be a real directory")
	}
	expected := make(map[string]bool, len(groups))
	for _, group := range groups {
		if group == "" || group == "." || group == ".." || filepath.Base(group) != group || strings.ContainsAny(group, `/\\`) {
			return nil, fmt.Errorf("invalid release file group %q", group)
		}
		if expected[group] {
			return nil, fmt.Errorf("duplicate release file group %q", group)
		}
		expected[group] = true
	}
	entries, err := os.ReadDir(filesDir)
	if err != nil {
		return nil, fmt.Errorf("read release files-dir: %w", err)
	}
	files := make([]string, 0)
	basenames := map[string]bool{}
	for _, entry := range entries {
		if !expected[entry.Name()] {
			return nil, fmt.Errorf("unexpected release file group %q", entry.Name())
		}
		groupDir := filepath.Join(filesDir, entry.Name())
		info, err := os.Lstat(groupDir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("release file group %q must be a real directory: %v", entry.Name(), err)
		}
		contents, err := os.ReadDir(groupDir)
		if err != nil {
			return nil, fmt.Errorf("read release file group %q: %w", entry.Name(), err)
		}
		if len(contents) == 0 {
			return nil, fmt.Errorf("release file group %q is empty", entry.Name())
		}
		for _, asset := range contents {
			path := filepath.Join(groupDir, asset.Name())
			file, err := os.Lstat(path)
			if err != nil {
				return nil, fmt.Errorf("inspect release file %s: %w", path, err)
			}
			if !file.Mode().IsRegular() || file.Size() == 0 {
				return nil, fmt.Errorf("release file %s must be a nonempty regular file", path)
			}
			if basenames[asset.Name()] {
				return nil, fmt.Errorf("duplicate release asset name %q", asset.Name())
			}
			basenames[asset.Name()] = true
			files = append(files, path)
		}
		delete(expected, entry.Name())
	}
	for group := range expected {
		return nil, fmt.Errorf("missing release file group %q", group)
	}
	return files, nil
}

func runGoReleaser(ctx context.Context, root string, plugins []PackageManagerBackend, snapshot bool, stdout, stderr io.Writer) error {
	return runGoReleaserWithFiles(ctx, root, plugins, nil, snapshot, stdout, stderr)
}

func runGoReleaserWithFiles(ctx context.Context, root string, plugins []PackageManagerBackend, files []string, snapshot bool, stdout, stderr io.Writer) error {
	manifest, _, err := loadReleaseBackends(root)
	if err != nil {
		return err
	}
	configuration, err := goReleaserConfigWithFiles(root, manifest, plugins, files)
	if err != nil {
		return fmt.Errorf("generate GoReleaser configuration: %w", err)
	}
	if len(configuration) == 0 {
		fmt.Fprintln(stdout, "skip: workspace has no downloadable release artifacts")
		return nil
	}
	workingDirectory, serial, err := prepareReleaseWorkspace(ctx, root, manifest, plugins, stdout, stderr)
	if err != nil {
		return err
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
	if serial {
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
	if !snapshot {
		if token := goreleaserToken(); token != "" {
			releaseEnvironment = append(releaseEnvironment, "GITHUB_TOKEN="+token)
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

func goreleaserToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("GH_TOKEN")
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

func createAndPushReleaseTag(ctx context.Context, root, version string, auth transport.AuthMethod) (resultErr error) {
	if !releaseVersionPattern.MatchString(version) {
		return fmt.Errorf("release version %q is not a stable or RC vMAJOR.MINOR.PATCH tag", version)
	}
	if auth == nil {
		return errors.New("GITHUB_TOKEN or GH_TOKEN is required to push a release tag")
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
		return fmt.Errorf("create release tag %s: %w", version, err)
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if err := repository.DeleteTag(version); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove unpushed local release tag %s: %w", version, err))
		}
	}()

	remote, err := repository.Remote("origin")
	if err != nil {
		return fmt.Errorf("open origin remote: %w", err)
	}
	refspec := gitconfig.RefSpec("refs/tags/" + version + ":refs/tags/" + version)
	if err := remote.PushContext(ctx, &git.PushOptions{Auth: auth, RefSpecs: []gitconfig.RefSpec{refspec}}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("push release tag %s: %w", version, err)
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
