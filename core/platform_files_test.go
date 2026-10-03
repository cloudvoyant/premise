package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlatformFileHandoff(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(t.TempDir(), "artifacts")
	t.Setenv("RELEASE_VERSION", "0.2.2-rc.98")
	t.Setenv("PREMISE_RELEASE_CHANNEL", "rc")
	t.Setenv("PREMISE_ARTIFACT_DIR", output)
	t.Setenv("GH_TOKEN", "") // Building files must not need publishing credentials.

	if err := PreparePlatformConfig(root, "build"); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(root, "target", "premise-release-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(config, &decoded); err != nil || decoded["version"] != "0.2.2-rc.98" {
		t.Fatalf("version override = %q, error = %v", config, err)
	}
	bundle := filepath.Join(root, "target", "release", "bundle", "dmg")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "app.dmg"), []byte("installer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "debug.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CollectPlatformFiles(root, "build", "target/release/bundle", ".dmg,.msi"); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(output)
	if err != nil || len(files) != 1 || files[0].Name() != "app.dmg" {
		t.Fatalf("artifact handoff = %v, error = %v", files, err)
	}
	if err := CollectPlatformFiles(root, "build", "target/release/bundle", ".dmg"); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("staging over an existing file must fail, got %v", err)
	}
}

func TestPlatformCargoWorkspaceSource(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("Cargo is not installed")
	}
	workspace := t.TempDir()
	project := filepath.Join(workspace, "app")
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		filepath.Join(workspace, "Cargo.toml"):  "[workspace]\nmembers = [\"app\"]\nresolver = \"2\"\n",
		filepath.Join(project, "Cargo.toml"):    "[package]\nname = \"platform-test\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		filepath.Join(project, "src", "lib.rs"): "pub fn test() {}\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("RELEASE_VERSION", "0.2.2-rc.98")
	t.Setenv("PREMISE_RELEASE_CHANNEL", "rc")
	output := filepath.Join(t.TempDir(), "artifacts")
	t.Setenv("PREMISE_ARTIFACT_DIR", output)
	bundle := filepath.Join(workspace, "target", "release", "bundle")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(bundle, "old.dmg")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PreparePlatformConfig(project, "build"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale installer survived preparation: %v", err)
	}
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "current.dmg"), []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CollectPlatformFiles(project, "build", "cargo:release/bundle", ".dmg"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "current.dmg")); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformFileValidation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RELEASE_VERSION", "0.2.2-rc.98")
	t.Setenv("PREMISE_RELEASE_CHANNEL", "stable")
	t.Setenv("PREMISE_ARTIFACT_DIR", filepath.Join(t.TempDir(), "output"))
	if err := PreparePlatformConfig(root, "build"); err == nil {
		t.Fatal("stable mode accepted an RC version")
	}
	t.Setenv("PREMISE_RELEASE_CHANNEL", "rc")
	for _, source := range []string{"../outside", "/tmp/outside"} {
		if err := CollectPlatformFiles(root, "build", source, ".dmg"); err == nil {
			t.Fatalf("accepted unsafe source %q", source)
		}
	}
	if err := CollectPlatformFiles(root, "build", "target/release/bundle", "dmg"); err == nil {
		t.Fatal("accepted a suffix without a dot")
	}
	if err := CollectPlatformFiles(root, "publish:rc", "target/release/bundle", ".dmg"); err == nil {
		t.Fatal("accepted publishing without a repository and token")
	}
}
