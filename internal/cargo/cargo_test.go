package cargo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsRegistry(t *testing.T) {
	root := t.TempDir()
	if registry, err := IsRegistry(root); err != nil || registry {
		t.Fatalf("IsRegistry(empty) = %v, %v; want false", registry, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if registry, err := IsRegistry(root); err != nil || !registry {
		t.Fatalf("IsRegistry(Cargo) = %v, %v; want true", registry, err)
	}
}

func TestPublishUsesCargoCredentialsOnly(t *testing.T) {
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "capture")
	shim := `#!/bin/sh
set -eu
[ "$*" = "run publish" ]
[ -z "${GITHUB_TOKEN:-}" ]
[ -z "${GH_TOKEN:-}" ]
[ -z "${CRATES_TOKEN:-}" ]
printf 'cargo:%s\nversion:%s\n' "${CARGO_REGISTRY_TOKEN:-}" "${RELEASE_VERSION:-}" > "$CAPTURE"
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GITHUB_TOKEN", "github-secret")
	t.Setenv("GH_TOKEN", "gh-secret")
	t.Setenv("CARGO_REGISTRY_TOKEN", "cargo-secret")
	t.Setenv("CRATES_TOKEN", "raw-cargo-secret")
	var output bytes.Buffer
	if err := Publish(t.Context(), t.TempDir(), "v1.2.3", &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(captured)), "cargo:cargo-secret\nversion:v1.2.3"; got != want {
		t.Fatalf("Publish() capture = %q, want %q", got, want)
	}
}
