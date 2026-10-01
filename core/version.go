package core

// Version module responsibilities:
//   - calculate current and next stable versions through the svu Go SDK;
//   - validate explicit semantic-version bumps and RC identifiers;
//   - restrict repository baselines to strict stable vMAJOR.MINOR.PATCH tags.

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/caarlos0/svu/v3/pkg/svu"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

var (
	versionDirectoryMu  sync.Mutex
	rcIdentifierPattern = regexp.MustCompile(`^[0-9A-Za-z-]+$`)
)

// VersionBump identifies an explicit semantic-version component to increment.
type VersionBump string

const (
	VersionBumpPatch VersionBump = "patch"
	VersionBumpMinor VersionBump = "minor"
	VersionBumpMajor VersionBump = "major"
)

// ValidatePackagePublicationVersion validates a stable or release-candidate
// package version and returns it without the optional leading v.
func ValidatePackagePublicationVersion(version, task string) (string, error) {
	normalized := strings.TrimPrefix(version, "v")
	parsed, err := semver.StrictNewVersion(normalized)
	if err != nil {
		return "", fmt.Errorf("invalid package release version %q: %w", normalized, err)
	}
	if parsed.Metadata() != "" {
		return "", fmt.Errorf("package %s requires a version without build metadata, got %s", task, normalized)
	}
	switch task {
	case "publish":
		if parsed.Prerelease() != "" {
			return "", fmt.Errorf("package publish requires a stable version, got %s", normalized)
		}
	case "publish:rc":
		if parsed.Prerelease() == "" {
			return "", fmt.Errorf("package publish:rc requires a prerelease version, got %s", normalized)
		}
	default:
		return "", fmt.Errorf("unsupported package publication task %q", task)
	}
	return normalized, nil
}

// ParseVersionBump validates a user-provided semantic-version component.
func ParseVersionBump(value string) (VersionBump, error) {
	bump := VersionBump(value)
	switch bump {
	case VersionBumpPatch, VersionBumpMinor, VersionBumpMajor:
		return bump, nil
	default:
		return "", fmt.Errorf("invalid bump %q: expected patch, minor, or major", value)
	}
}

// ValidateRCIdentifier validates one SemVer prerelease identifier. Numeric
// identifiers must not contain leading zeroes.
func ValidateRCIdentifier(identifier string) error {
	if !rcIdentifierPattern.MatchString(identifier) {
		return fmt.Errorf("invalid release-candidate identifier %q: use ASCII letters, digits, or hyphens", identifier)
	}
	if isASCIIDigits(identifier) && len(identifier) > 1 && identifier[0] == '0' {
		return fmt.Errorf("invalid release-candidate identifier %q: numeric identifiers must not contain leading zeroes", identifier)
	}
	return nil
}

// CurrentVersion returns the latest stable version tag reachable in the
// repository. The svu SDK performs the calculation without invoking its CLI.
func CurrentVersion(root string) (string, error) {
	version, err := calculateVersion(root, versionActionCurrent)
	if err != nil {
		return "", fmt.Errorf("calculate current version: %w", err)
	}
	return version, nil
}

// NextVersion returns the next version inferred from conventional commits.
func NextVersion(root string) (string, error) {
	version, err := calculateVersion(root, versionActionNext)
	if err != nil {
		return "", fmt.Errorf("calculate next version: %w", err)
	}
	return version, nil
}

// BumpedVersion increments the requested version component.
func BumpedVersion(root string, bump VersionBump) (string, error) {
	var action versionAction
	switch bump {
	case VersionBumpPatch:
		action = versionActionPatch
	case VersionBumpMinor:
		action = versionActionMinor
	case VersionBumpMajor:
		action = versionActionMajor
	default:
		return "", fmt.Errorf("invalid bump %q: expected patch, minor, or major", bump)
	}
	version, err := calculateVersion(root, action)
	if err != nil {
		return "", fmt.Errorf("calculate %s version: %w", bump, err)
	}
	return version, nil
}

// ReleaseCandidateVersion returns the next repository-derived version with an
// rc.<identifier> prerelease suffix.
func ReleaseCandidateVersion(root, identifier string) (string, error) {
	if err := ValidateRCIdentifier(identifier); err != nil {
		return "", fmt.Errorf("validate release candidate: %w", err)
	}
	version, err := calculateVersion(root, versionActionNext, "rc."+identifier)
	if err != nil {
		return "", fmt.Errorf("calculate release-candidate version: %w", err)
	}
	return version, nil
}

type versionAction uint8

const (
	versionActionCurrent versionAction = iota
	versionActionNoBump
	versionActionNext
	versionActionPatch
	versionActionMinor
	versionActionMajor
)

var (
	breakingBodyPattern  = regexp.MustCompile(`(?m).*BREAKING[ -]CHANGE:.*`)
	breakingTitlePattern = regexp.MustCompile(`(?im).*(\w+)(\(.*\))?!:.*`)
	featurePattern       = regexp.MustCompile(`(?im).*feat(\(.*\))?:.*`)
	fixPattern           = regexp.MustCompile(`(?im).*fix(\(.*\))?:.*`)
)

func calculateVersion(root string, action versionAction, prerelease ...string) (string, error) {
	stableTag, err := latestStableVersionTag(root)
	if err != nil {
		return "", fmt.Errorf("resolve stable version baseline: %w", err)
	}
	if stableTag == "" {
		return calculateVirtualBaseline(root, action, prerelease...)
	}
	var calculate func(...svu.Option) (string, error)
	switch action {
	case versionActionCurrent:
		calculate = svu.Current
	case versionActionNext:
		calculate = svu.Next
	case versionActionPatch:
		calculate = svu.Patch
	case versionActionMinor:
		calculate = svu.Minor
	case versionActionMajor:
		calculate = svu.Major
	}
	options := []svu.Option{
		svu.WithPattern(stableTag),
		svu.WithPrefix("v"),
		svu.ForAllBranches(),
		svu.WithDirectories("."),
	}
	for _, value := range prerelease {
		options = append(options, svu.WithPreRelease(value))
	}

	versionDirectoryMu.Lock()
	defer versionDirectoryMu.Unlock()

	previous, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	if err := os.Chdir(root); err != nil {
		return "", fmt.Errorf("enter repository %s: %w", root, err)
	}
	version, calculateErr := calculate(options...)
	restoreErr := os.Chdir(previous)
	if calculateErr != nil {
		return "", fmt.Errorf("calculate repository version: %w", calculateErr)
	}
	if restoreErr != nil {
		return "", fmt.Errorf("restore working directory %s: %w", previous, restoreErr)
	}
	return version, nil
}

func calculateVirtualBaseline(root string, action versionAction, prerelease ...string) (string, error) {
	if action == versionActionCurrent {
		return "v0.0.0", nil
	}
	repository, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", fmt.Errorf("open version repository: %w", err)
	}
	head, err := repository.Head()
	if err != nil {
		return "", fmt.Errorf("resolve repository HEAD: %w", err)
	}
	commits, err := repository.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return "", fmt.Errorf("scan repository commits: %w", err)
	}
	defer commits.Close()
	bump := versionActionNoBump
	for {
		commit, err := commits.Next()
		if err == io.EOF || err == plumbing.ErrObjectNotFound {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read repository commit: %w", err)
		}
		message := commit.Message
		switch {
		case breakingBodyPattern.MatchString(message) || breakingTitlePattern.MatchString(message):
			bump = versionActionMajor
		case bump < versionActionMinor && featurePattern.MatchString(message):
			bump = versionActionMinor
		case fixPattern.MatchString(message):
			// Keep the patch bump.
		}
	}
	if action != versionActionNext {
		bump = action
	}
	version := *semver.MustParse("0.0.0")
	switch bump {
	case versionActionMajor:
		version = version.IncMajor()
	case versionActionMinor:
		version = version.IncMinor()
	case versionActionPatch:
		version = version.IncPatch()
	}
	result := "v" + version.String()
	if len(prerelease) > 0 {
		result += "-" + prerelease[0]
	}
	return result, nil
}

func latestStableVersionTag(root string) (string, error) {
	repository, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", fmt.Errorf("open version repository: %w", err)
	}
	tags, err := repository.Tags()
	if err != nil {
		return "", fmt.Errorf("list version tags: %w", err)
	}
	var latest *semver.Version
	latestTag := ""
	if err := tags.ForEach(func(reference *plumbing.Reference) error {
		name := reference.Name().Short()
		if !stableVersionPattern.MatchString(name) {
			return nil
		}
		candidate, err := semver.StrictNewVersion(strings.TrimPrefix(name, "v"))
		if err != nil {
			return nil
		}
		if latest == nil || candidate.GreaterThan(latest) {
			latest = candidate
			latestTag = name
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("inspect version tags: %w", err)
	}
	if latestTag == "" {
		return "", nil
	}
	return latestTag, nil
}

func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}
