package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// svuCommand is the svu CLI binary executed for version calculations. It is a
// package variable so tests can substitute a fake binary.
var svuCommand = "svu"

// rcIdentifierPattern matches a single SemVer prerelease identifier: one or
// more ASCII letters, digits, or hyphens.
var rcIdentifierPattern = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

// versionCmd groups the svu-backed version calculation commands.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Calculate release versions using the pinned svu CLI",
	Args:  cobra.NoArgs,
}

var versionCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print the current version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runVersionCommand(cmd, []string{"current"})
	},
}

var versionNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Print the next version based on git history",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runVersionCommand(cmd, []string{"next"})
	},
}

var versionBumpCmd = &cobra.Command{
	Use:   "bump patch|minor|major",
	Short: "Print the next patch, minor, or major version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		svuArgs, err := svuBumpArgs(args[0])
		if err != nil {
			return err
		}
		return runVersionCommand(cmd, svuArgs)
	},
}

var rcIdentifier string

var versionRcCmd = &cobra.Command{
	Use:   "rc",
	Short: "Print the next release-candidate version",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		svuArgs, err := svuRCArgs(rcIdentifier)
		if err != nil {
			return err
		}
		return runVersionCommand(cmd, svuArgs)
	},
}

func init() {
	versionRcCmd.Flags().StringVar(&rcIdentifier, "identifier", "", "release-candidate identifier (for example a build number)")
	_ = versionRcCmd.MarkFlagRequired("identifier")
	versionCmd.AddCommand(versionCurrentCmd, versionNextCmd, versionBumpCmd, versionRcCmd)
	rootCmd.AddCommand(versionCmd)
}

// runVersionCommand executes svu and writes its single-line version to stdout.
func runVersionCommand(cmd *cobra.Command, args []string) error {
	version, err := runSvu(cmd.Context(), args...)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), version)
	return nil
}

// runSvu executes the pinned svu CLI and returns exactly one version string.
func runSvu(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, svuCommand, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("svu %s: %s", strings.Join(args, " "), message)
	}
	version := strings.TrimSpace(stdout.String())
	if version == "" {
		return "", errors.New("svu produced no output")
	}
	if strings.Contains(version, "\n") {
		return "", fmt.Errorf("svu produced multiple output lines")
	}
	return version, nil
}

// svuBumpArgs translates a bump keyword into the matching svu subcommand.
func svuBumpArgs(bump string) ([]string, error) {
	switch bump {
	case "patch", "minor", "major":
		return []string{bump}, nil
	default:
		return nil, fmt.Errorf("bump must be one of patch, minor, or major (got %q)", bump)
	}
}

// svuRCArgs translates an RC identifier into an svu next-version invocation
// that derives MAJOR.MINOR.PATCH-rc.<id> from conventional commits since the
// current stable tag.
func svuRCArgs(identifier string) ([]string, error) {
	if err := validateRCIdentifier(identifier); err != nil {
		return nil, err
	}
	return []string{"next", "--prerelease", "rc." + identifier}, nil
}

// validateRCIdentifier reports whether id is a valid SemVer prerelease
// identifier: non-empty ASCII alphanumerics and hyphens, with numeric
// identifiers forbidding leading zeroes.
func validateRCIdentifier(id string) error {
	if id == "" {
		return errors.New("rc identifier is required")
	}
	if !rcIdentifierPattern.MatchString(id) {
		return fmt.Errorf("invalid rc identifier %q: must contain only letters, digits, and hyphens", id)
	}
	if len(id) > 1 && id[0] == '0' && isASCIIDigits(id) {
		return fmt.Errorf("invalid rc identifier %q: numeric identifiers must not contain leading zeroes", id)
	}
	return nil
}

func isASCIIDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
