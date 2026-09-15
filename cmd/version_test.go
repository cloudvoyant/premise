package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestValidateRCIdentifier(t *testing.T) {
	valid := []string{
		"1",
		"5",
		"0",
		"10",
		"rc1",
		"beta",
		"build-42",
		"a1b2c3",
	}
	for _, id := range valid {
		if err := validateRCIdentifier(id); err != nil {
			t.Errorf("validateRCIdentifier(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{
		"",
		"05",        // numeric with leading zero
		"00",        // numeric with leading zeroes
		"rc.1",      // dot is not a single identifier
		"has space", // spaces
		"a_b",       // underscore
		"a+b",       // build metadata separator
		"é",         // non-ASCII
	}
	for _, id := range invalid {
		if err := validateRCIdentifier(id); err == nil {
			t.Errorf("validateRCIdentifier(%q) = nil, want error", id)
		}
	}
}

func TestSvuBumpArgs(t *testing.T) {
	for bump, want := range map[string]string{"patch": "patch", "minor": "minor", "major": "major"} {
		args, err := svuBumpArgs(bump)
		if err != nil {
			t.Fatalf("svuBumpArgs(%q): %v", bump, err)
		}
		if len(args) != 1 || args[0] != want {
			t.Fatalf("svuBumpArgs(%q) = %v, want [%s]", bump, args, want)
		}
	}

	for _, bad := range []string{"", "Patch", "hotfix", "patch minor"} {
		if _, err := svuBumpArgs(bad); err == nil {
			t.Errorf("svuBumpArgs(%q) = nil, want error", bad)
		}
	}
}

func TestSvuRCArgs(t *testing.T) {
	args, err := svuRCArgs("5")
	if err != nil {
		t.Fatalf("svuRCArgs(5): %v", err)
	}
	want := []string{"next", "--prerelease", "rc.5"}
	if len(args) != len(want) {
		t.Fatalf("svuRCArgs(5) = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("svuRCArgs(5) = %v, want %v", args, want)
		}
	}

	if _, err := svuRCArgs("05"); err == nil {
		t.Fatal("svuRCArgs(05) = nil, want leading-zero error")
	}
}

// TestRunSvuVersions exercises the configured svu CLI against a real git
// repository. It is skipped when svu or git is unavailable.
func TestRunSvuVersions(t *testing.T) {
	if _, err := exec.LookPath("svu"); err != nil {
		t.Skip("svu not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repo}, args...)
		command := exec.Command("git", full...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	config := `tag:
  pattern: "v[0-9]*.[0-9]*.[0-9]*"
  prefix: "v"
  mode: all
`
	if err := os.WriteFile(filepath.Join(repo, ".svu.yml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write .svu.yml: %v", err)
	}
	runGit("init", "-q")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "test")
	runGit("add", ".svu.yml")
	runGit("commit", "-q", "-m", "chore: configure versioning")
	runGit("tag", "v0.1.0")
	runGit("commit", "-q", "--allow-empty", "-m", "feat: add release feature")
	runGit("tag", "pre-squash/feature/test")

	t.Chdir(repo)
	assertVersion := func(want string, args ...string) {
		t.Helper()
		got, err := runSvu(context.Background(), args...)
		if err != nil {
			t.Fatalf("runSvu %v: %v", args, err)
		}
		if got != want {
			t.Fatalf("runSvu %v = %q, want %q", args, got, want)
		}
	}

	assertVersion("v0.1.0", "current")
	assertVersion("v0.2.0", "next")
	assertVersion("v0.2.0-rc.42", "next", "--prerelease", "rc.42")
	assertVersion("v1.0.0", "major")
}
