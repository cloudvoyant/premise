package core

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlatformFlowValidation(t *testing.T) {
	root := writeCIProject(t, []Template{templateFixture("app", "app")}, false)
	output := filepath.Join(t.TempDir(), "output")
	for _, test := range []struct {
		name, project, channel, version, output string
		mode                                    CIReleaseMode
	}{
		{"release mode", "app", "stable", "0.2.2", output, CIReleaseAuto},
		{"unknown project", "missing", "stable", "0.2.2", output, CIReleaseNone},
		{"invalid channel", "app", "beta", "0.2.2", output, CIReleaseNone},
		{"invalid version", "app", "stable", "0.2.2-rc.1", output, CIReleaseNone},
		{"relative output", "app", "stable", "0.2.2", "relative", CIReleaseNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := RunPlatformFlow(context.Background(), root, test.project, test.channel,
				test.version, test.output, test.mode, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
				t.Fatal("accepted an invalid platform build")
			}
		})
	}
}

func TestPlatformFlowRunsOnlyBuildTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture uses POSIX redirection; the product task is cross-platform")
	}
	if _, err := exec.LookPath("mise"); err != nil {
		t.Skip("mise is not installed")
	}
	root := writeCIProject(t, []Template{templateFixture("app", "app")}, false)
	template := filepath.Join(root, "templates", "app")
	if err := os.MkdirAll(template, 0o755); err != nil {
		t.Fatal(err)
	}
	config := "[tasks.\"release:build\"]\n" +
		"run = 'echo native > \"$PREMISE_ARTIFACT_DIR/installer.zip\"'\n"
	if err := os.WriteFile(filepath.Join(template, "mise.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "native")
	var stdout, stderr bytes.Buffer
	if err := RunPlatformFlow(context.Background(), root, "app", "rc", "0.2.2-rc.98",
		output, CIReleaseNone, &stdout, &stderr); err != nil {
		t.Fatalf("platform build: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	data, err := os.ReadFile(filepath.Join(output, "installer.zip"))
	if err != nil || strings.TrimSpace(string(data)) != "native" {
		t.Fatalf("native artifact = %q, error = %v", data, err)
	}
	if err := RunPlatformFlow(context.Background(), root, "app", "rc", "0.2.2-rc.98",
		output, CIReleaseNone, &stdout, &stderr); err == nil {
		t.Fatal("accepted stale output files on rerun")
	}
}
