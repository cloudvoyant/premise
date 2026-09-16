package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestValidateRCIdentifier(t *testing.T) {
	for _, identifier := range []string{"1", "5", "0", "10", "rc1", "beta", "build-42", "a1b2c3"} {
		if err := ValidateRCIdentifier(identifier); err != nil {
			t.Errorf("ValidateRCIdentifier(%q) = %v, want nil", identifier, err)
		}
	}
	for _, identifier := range []string{"", "05", "00", "rc.1", "has space", "a_b", "a+b", "é"} {
		if err := ValidateRCIdentifier(identifier); err == nil {
			t.Errorf("ValidateRCIdentifier(%q) = nil, want error", identifier)
		}
	}
}

func TestParseVersionBump(t *testing.T) {
	for _, value := range []string{"patch", "minor", "major"} {
		bump, err := ParseVersionBump(value)
		if err != nil {
			t.Fatalf("ParseVersionBump(%q): %v", value, err)
		}
		if string(bump) != value {
			t.Fatalf("ParseVersionBump(%q) = %q", value, bump)
		}
	}
	for _, value := range []string{"", "Patch", "hotfix", "patch minor"} {
		if _, err := ParseVersionBump(value); err == nil {
			t.Errorf("ParseVersionBump(%q) = nil, want error", value)
		}
	}
}

func TestRepositoryVersionsUseSvuSDK(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repository := t.TempDir()
	runGit := func(arguments ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", arguments, err, output)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "test")
	tracked := filepath.Join(repository, "README.md")
	if err := os.WriteFile(tracked, []byte("bootstrap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-q", "-m", "chore: bootstrap")
	runGit("tag", "v0.1.0")
	if err := os.WriteFile(tracked, []byte("bootstrap\nfeature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-q", "-m", "feat: add release feature")
	runGit("tag", "pre-squash/feature/test")
	runGit("tag", "v9.9.9-rc.1")
	runGit("tag", "v8.8.8+build")
	runGit("tag", "v01.99.99")

	assertVersion := func(name, want string, calculate func(string) (string, error)) {
		t.Helper()
		got, err := calculate(repository)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}

	assertVersion("CurrentVersion", "v0.1.0", CurrentVersion)
	assertVersion("NextVersion", "v0.2.0", NextVersion)
	assertVersion("ReleaseCandidateVersion", "v0.2.0-rc.42", func(root string) (string, error) {
		return ReleaseCandidateVersion(root, "42")
	})
	assertVersion("BumpedVersion", "v1.0.0", func(root string) (string, error) {
		return BumpedVersion(root, VersionBumpMajor)
	})
}
