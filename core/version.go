package core

// Version module responsibilities:
//   - calculate current and next stable versions through the svu Go SDK;
//   - validate explicit semantic-version bumps and RC identifiers;
//   - restrict repository baselines to strict stable vMAJOR.MINOR.PATCH tags.

import (
	"errors"
	"fmt"
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
	version, err := calculateVersion(root, svu.Current)
	if err != nil {
		return "", fmt.Errorf("calculate current version: %w", err)
	}
	return version, nil
}

// NextVersion returns the next version inferred from conventional commits.
func NextVersion(root string) (string, error) {
	version, err := calculateVersion(root, svu.Next)
	if err != nil {
		return "", fmt.Errorf("calculate next version: %w", err)
	}
	return version, nil
}

// BumpedVersion increments the requested version component.
func BumpedVersion(root string, bump VersionBump) (string, error) {
	var calculate func(...svu.Option) (string, error)
	switch bump {
	case VersionBumpPatch:
		calculate = svu.Patch
	case VersionBumpMinor:
		calculate = svu.Minor
	case VersionBumpMajor:
		calculate = svu.Major
	default:
		return "", fmt.Errorf("invalid bump %q: expected patch, minor, or major", bump)
	}
	version, err := calculateVersion(root, calculate)
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
	version, err := calculateVersion(root, svu.Next, svu.WithPreRelease("rc."+identifier))
	if err != nil {
		return "", fmt.Errorf("calculate release-candidate version: %w", err)
	}
	return version, nil
}

func calculateVersion(root string, calculate func(...svu.Option) (string, error), extra ...svu.Option) (string, error) {
	stableTag, err := latestStableVersionTag(root)
	if err != nil {
		return "", fmt.Errorf("resolve stable version baseline: %w", err)
	}
	options := []svu.Option{
		svu.WithPattern(stableTag),
		svu.WithPrefix("v"),
		svu.ForAllBranches(),
		svu.WithDirectories("."),
	}
	options = append(options, extra...)

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
		return "", errors.New("no stable vMAJOR.MINOR.PATCH tag found; create the v0.0.0 bootstrap tag")
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
