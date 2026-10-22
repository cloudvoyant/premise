package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
	"go.yaml.in/yaml/v3"
)

// Fixtures -------------------------------------------------------------------

func sourceReleaseFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	remote := filepath.Join(base, "origin.git")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(workspace, "premise.yaml"), `workspace:
  name: lua-plugin
  kind: monorepo
  schema-version: "0.2"
  package_managers: []
  providers: {ci: github}
  projects:
    - name: lua-plugin
      template: cloudvoyant/premise:premise-lib
      version: 0.1.0
      path: libs/lua-plugin
`, 0o644)
	writeFixtureFile(t, filepath.Join(workspace, "libs", "lua-plugin", "lua", "init.lua"), "return {}\n", 0o644)
	// Source-only publication must not invoke even an existing failing hook.
	writeFixtureFile(t, filepath.Join(workspace, "mise.toml"), "[tasks.publish]\nrun = 'exit 1'\n[tasks.\"publish:rc\"]\nrun = 'exit 1'\n", 0o644)
	git(t, workspace, "init", "-q")
	git(t, workspace, "config", "user.email", "test@example.invalid")
	git(t, workspace, "config", "user.name", "Test")
	git(t, workspace, "add", ".")
	git(t, workspace, "commit", "-qm", "feat: add Lua plugin")
	git(t, base, "init", "--bare", "-q", remote)
	git(t, workspace, "remote", "add", "origin", remote)
	git(t, workspace, "push", "-q", "origin", "HEAD")
	return workspace, remote
}

// Tests ----------------------------------------------------------------------

func TestSourceOnlyCLIAndActionRelease(t *testing.T) {
	root := repositoryRoot(t)
	bin := t.TempDir()
	binary := filepath.Join(bin, "pm")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	// These tools reject package hooks and check the actual generated release configuration.
	writeFixtureFile(t, filepath.Join(bin, "mise"), `#!/bin/sh
set -eu
[ -z "${GITHUB_TOKEN:-}" ]
[ -z "${GH_TOKEN:-}" ]
[ -z "${NODE_AUTH_TOKEN:-}" ]
[ -z "${CRATES_TOKEN:-}" ]
case "$*" in
 exec\ goreleaser@2.18.1\ --\ env\ -0) /usr/bin/env -0 ;;
 *) echo "unexpected mise invocation: $*" >&2; exit 1 ;;
esac
`, 0o755)
	writeFixtureFile(t, filepath.Join(bin, "goreleaser"), `#!/bin/sh
set -eu
[ -n "${GITHUB_TOKEN:-}" ]
[ -z "${GH_TOKEN:-}" ]
[ -z "${NODE_AUTH_TOKEN:-}" ]
[ -z "${CRATES_TOKEN:-}" ]
[ "$1" = release ]
while [ "$#" -gt 0 ]; do
 case "$1" in
  -f) shift; configuration=$1 ;;
  --snapshot) echo "source-only build should skip artifact tools" >&2; exit 1 ;;
 esac
 shift
done
[ "$(pwd -P)" = "$EXPECTED_RELEASE_ROOT" ]
grep -q 'skip: true' "$configuration"
grep -q 'disable: true' "$configuration"
! grep -q 'extra_files:' "$configuration"
printf 'publish\n' >> "$RELEASE_CAPTURE"
[ "${FAIL_PUBLICATION:-false}" != true ]
`, 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GH_TOKEN", "test-alias")
	t.Setenv("NODE_AUTH_TOKEN", "registry-secret")
	t.Setenv("CRATES_TOKEN", "registry-secret")
	capture := filepath.Join(t.TempDir(), "capture")
	t.Setenv("RELEASE_CAPTURE", capture)
	scripts := sourceReleaseActionScripts(t, root)
	for _, mode := range []string{"split-cli", "full-cli", "action"} {
		t.Run(mode, func(t *testing.T) {
			workspace, remote := sourceReleaseFixture(t)
			physical, err := filepath.EvalSymlinks(workspace)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("EXPECTED_RELEASE_ROOT", physical)
			plan := sourceReleaseCommand(t, workspace, binary, "release", "--dry-run")
			if !strings.Contains(plan, "version=0.1.0\nshould_publish=true") {
				t.Fatalf("plan = %s", plan)
			}
			// No credentials or package hooks are needed to build a source-only library.
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GH_TOKEN", "")
			out := sourceReleaseCommand(t, workspace, binary, "release", "--build")
			if !strings.Contains(out, "skip: workspace has no downloadable release artifacts") {
				t.Fatal(out)
			}
			if tags := gitOutput(t, remote, "tag"); tags != "" {
				t.Fatalf("build created remote tag: %s", tags)
			}
			t.Setenv("GITHUB_TOKEN", "test-token")
			t.Setenv("GH_TOKEN", "test-alias")
			publish := func(channel, version string) {
				t.Helper()
				if mode == "action" {
					runSourceReleaseAction(t, workspace, scripts["Run Premise operation"], "release", channel, version)
					return
				}
				if mode == "full-cli" && channel == "stable" {
					sourceReleaseCommand(t, workspace, binary, "release")
					return
				}
				sourceReleaseCommand(t, workspace, binary, "release", "publish", "--channel", channel, "--expected-version", version)
			}
			packages := func(channel, version string) {
				t.Helper()
				var output string
				if mode == "action" {
					output = runSourceReleaseAction(t, workspace, scripts["Run Premise operation"], "packages", channel, version)
				} else {
					output = sourceReleaseCommand(t, workspace, binary, "release", "packages", "--channel", channel, "--expected-version", version)
				}
				if !strings.Contains(output, "skip: workspace package managers do not publish language packages") {
					t.Fatal(output)
				}
			}
			if mode == "action" {
				t.Setenv("GITHUB_OUTPUT", filepath.Join(t.TempDir(), "output"))
				runSourceReleaseAction(t, workspace, scripts["Plan stable release"], "plan", "stable", "")
				runSourceReleaseAction(t, workspace, scripts["Plan runner schedule"], "plan", "stable", "")
				output, err := os.ReadFile(os.Getenv("GITHUB_OUTPUT"))
				if err != nil || !strings.Contains(string(output), "should_publish=true") || !strings.Contains(string(output), "native=false") {
					t.Fatalf("action plan: %s, %v", output, err)
				}
			}
			t.Setenv("FAIL_PUBLICATION", "true")
			// Publication failure is visible; its already-pushed tag enables a retry.
			arguments := []string{"release", "publish", "--channel", "stable", "--expected-version", "v0.1.0"}
			if mode == "full-cli" {
				arguments = []string{"release"}
			}
			command := exec.Command(binary, arguments...)
			command.Dir = workspace
			if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "run GoReleaser") {
				t.Fatalf("failed release = %v, %s", err, output)
			}
			head := gitOutput(t, workspace, "rev-parse", "HEAD")
			if tag := gitOutput(t, remote, "rev-parse", "refs/tags/v0.1.0"); tag != head {
				t.Fatalf("remote stable tag = %s, want %s", tag, head)
			}
			t.Setenv("FAIL_PUBLICATION", "false")
			publish("stable", "v0.1.0")
			packages("stable", "v0.1.0")
			publish("stable", "v0.1.0")
			git(t, workspace, "commit", "--allow-empty", "-qm", "fix: next release [publish-rc]")
			rc := strings.TrimSpace(sourceReleaseCommand(t, workspace, binary, "version", "rc", "--identifier", "42"))
			if rc != "v0.1.0-rc.42" {
				t.Fatalf("RC version = %s", rc)
			}
			if mode == "action" {
				t.Setenv("MARKED_PUSH", "false")
				t.Setenv("GITHUB_RUN_NUMBER", "42")
				runSourceReleaseAction(t, workspace, scripts["Plan release candidate"], "plan", "rc", rc)
				unmarked, err := os.ReadFile(os.Getenv("GITHUB_OUTPUT"))
				if err != nil || !strings.Contains(string(unmarked), "should_publish_rc=false") {
					t.Fatalf("unmarked action RC plan: %s, %v", unmarked, err)
				}
				t.Setenv("MARKED_PUSH", "true")
				runSourceReleaseAction(t, workspace, scripts["Plan release candidate"], "plan", "rc", rc)
				output, err := os.ReadFile(os.Getenv("GITHUB_OUTPUT"))
				if err != nil || !strings.Contains(string(output), "should_publish_rc=true") {
					t.Fatalf("action RC plan: %s, %v", output, err)
				}
			}
			publish("rc", rc)
			packages("rc", rc)
			publish("rc", rc)
			if tag := gitOutput(t, remote, "rev-parse", "refs/tags/"+rc); tag != gitOutput(t, workspace, "rev-parse", "HEAD") {
				t.Fatalf("RC tag = %s", tag)
			}
			// Existing RC tags away from HEAD must still fail without moving either tag.
			git(t, workspace, "commit", "--allow-empty", "-qm", "fix: another change")
			conflict := exec.Command(binary, "release", "publish", "--channel", "rc", "--expected-version", rc)
			conflict.Dir = workspace
			if output, err := conflict.CombinedOutput(); err == nil || !strings.Contains(string(output), "exists away from HEAD") {
				t.Fatalf("tag conflict = %v, %s", err, output)
			}
		})
	}
	data, err := os.ReadFile(capture)
	if err != nil || strings.Count(string(data), "publish\n") != 15 {
		t.Fatalf("release invocations = %s, %v", data, err)
	}
}

// TestSourceOnlyConfigWithGoReleaser validates with the optional, pinned local binary.
// Run with PREMISE_TEST_GORELEASER set to the GoReleaser 2.18.1 executable.
func TestSourceOnlyConfigWithGoReleaser(t *testing.T) {
	binary := os.Getenv("PREMISE_TEST_GORELEASER")
	if binary == "" {
		t.Skip("set PREMISE_TEST_GORELEASER to validate with GoReleaser 2.18.1")
	}
	version := sourceReleaseCommand(t, t.TempDir(), binary, "--version")
	if !strings.Contains(version, "2.18.1") {
		t.Fatalf("unexpected GoReleaser version: %s", version)
	}
	workspace, _ := sourceReleaseFixture(t)
	config, err := core.GoReleaserConfig(workspace)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "goreleaser.yml")
	writeFixtureFile(t, path, string(config), 0o600)
	sourceReleaseCommand(t, workspace, binary, "check", "-f", path)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	sourceReleaseCommand(t, workspace, binary, "release", "--snapshot", "--clean", "-f", path)
	matches, err := filepath.Glob(filepath.Join(workspace, "dist", "*checksums*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("source-only checksums = %v, %v", matches, err)
	}
	if tags := gitOutput(t, workspace, "tag"); tags != "" {
		t.Fatalf("snapshot created tag: %s", tags)
	}
}

// Private helpers ------------------------------------------------------------

func sourceReleaseCommand(t *testing.T, directory, binary string, args ...string) string {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", binary, args, err, output)
	}
	return string(output)
}

func sourceReleaseActionScripts(t *testing.T, root string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct{ Steps []struct{ Name, Run string } }
	}
	if err := yaml.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{}
	for _, step := range action.Runs.Steps {
		scripts[step.Name] = step.Run
	}
	for _, name := range []string{"Plan stable release", "Plan release candidate", "Plan runner schedule", "Run Premise operation"} {
		if scripts[name] == "" {
			t.Fatalf("action step %q missing", name)
		}
	}
	return scripts
}

func runSourceReleaseAction(t *testing.T, workspace, script, operation, channel, version string) string {
	t.Helper()
	t.Setenv("COMMAND", operation)
	flow := "on-merge"
	if channel == "rc" {
		flow = "on-commit"
	}
	t.Setenv("FLOW", flow)
	t.Setenv("RELEASE_MODE", "auto")
	t.Setenv("CHANNEL", channel)
	t.Setenv("VERSION", version)
	for _, key := range []string{"DEPLOY_ENVIRONMENT", "PREVIEW_BRANCH", "PROJECT", "PLATFORM", "OUTPUT_DIR", "FILES_DIR", "ROOT_ONLY", "SKIP_ROOT"} {
		t.Setenv(key, "")
	}
	return sourceReleaseCommand(t, workspace, "bash", "-c", script)
}
