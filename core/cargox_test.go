package core

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsRegistry(t *testing.T) {
	root := t.TempDir()
	if registry, err := isCargoRegistry(root); err != nil || registry {
		t.Fatalf("isCargoRegistry(empty) = %v, %v; want false", registry, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if registry, err := isCargoRegistry(root); err != nil || !registry {
		t.Fatalf("isCargoRegistry(templates/Cargo.toml) = %v, %v; want true", registry, err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if registry, err := isCargoRegistry(root); err != nil || !registry {
		t.Fatalf("isCargoRegistry(Cargo.toml) = %v, %v; want true", registry, err)
	}
}

func TestCargoPublicationPreflightsEveryPackageBeforePublishing(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.Templates = []Template{
		templateFixture("a-first", "lib"),
		templateFixture("z-second", "lib"),
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	for name, packageName := range map[string]string{"a-first": "a-first", "z-second": "unexpected-name"} {
		directory := filepath.Join(root, "templates", name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "[package]\nname = \"" + packageName + "\"\nversion = \"0.1.0\"\n"
		if err := os.WriteFile(filepath.Join(directory, "Cargo.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\nmembers = [\"templates/a-first\", \"templates/z-second\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.lock"), []byte("original lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	originalAPI := cratesAPIBaseURL
	cratesAPIBaseURL = server.URL
	defer func() { cratesAPIBaseURL = originalAPI }()

	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "published")
	shim := `#!/bin/sh
set -eu
case "$*" in
  "exec -- cargo generate-lockfile") printf 'generated lock\n' > Cargo.lock ;;
  "task info publish --json") ;;
  "run publish") printf 'published\n' > "$CAPTURE" ;;
  *) exit 9 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("CRATES_TOKEN", "cargo-secret")
	var output bytes.Buffer
	err := publishCargoPackages(t.Context(), root, "v1.2.3", "publish", &output, &output)
	if err == nil || !strings.Contains(err.Error(), `does not match declared template "z-second"`) {
		t.Fatalf("publishCargoPackages() error = %v, want second-package preflight failure", err)
	}
	if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication started before preflight completed: %v", err)
	}
}

func TestPublishUsesCargoCredentialsAndRestoresVersions(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.Templates = []Template{templateFixture("example-crate", "lib")}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	templateRoot := filepath.Join(root, "templates", "example-crate")
	if err := os.MkdirAll(templateRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cargoManifest := "[package]\nname = \"example-crate\"\nversion = \"0.1.0\"\n"
	if err := os.WriteFile(filepath.Join(templateRoot, "Cargo.toml"), []byte(cargoManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\nmembers = [\"templates/example-crate\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, "Cargo.lock")
	if err := os.WriteFile(lockPath, []byte("original lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/crates/example-crate":
			_, _ = response.Write([]byte(`{"crate":{"id":"example-crate"}}`))
		case "/crates/example-crate/1.2.3":
			response.WriteHeader(http.StatusNotFound)
		case "/me":
			if request.Header.Get("Authorization") != "cargo-secret" {
				t.Errorf("Authorization = %q, want cargo-secret", request.Header.Get("Authorization"))
			}
			_, _ = response.Write([]byte(`{"user":{"id":42}}`))
		case "/crates/example-crate/owners":
			if request.Header.Get("Authorization") != "cargo-secret" {
				t.Errorf("Authorization = %q, want cargo-secret", request.Header.Get("Authorization"))
			}
			_, _ = response.Write([]byte(`{"users":[{"id":42}]}`))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	originalAPI := cratesAPIBaseURL
	cratesAPIBaseURL = server.URL
	defer func() { cratesAPIBaseURL = originalAPI }()

	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "capture")
	shim := `#!/bin/sh
set -eu
case "$*" in
  "task info publish --json")
    [ -z "${CRATES_TOKEN:-}" ]
    [ -z "${CARGO_REGISTRY_TOKEN:-}" ]
    ;;
  "exec -- cargo generate-lockfile")
    [ -z "${CRATES_TOKEN:-}" ]
    [ -z "${CARGO_REGISTRY_TOKEN:-}" ]
    printf 'generated lock\n' > Cargo.lock
    ;;
  "run publish")
    [ -z "${GITHUB_TOKEN:-}" ]
    [ -z "${CRATES_TOKEN:-}" ]
    printf 'cargo:%s\nversion:%s\n' "${CARGO_REGISTRY_TOKEN:-}" "${RELEASE_VERSION:-}" > "$CAPTURE"
    ;;
  *) exit 9 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("GITHUB_TOKEN", "github-secret")
	t.Setenv("GH_TOKEN", "gh-secret")
	t.Setenv("CARGO_TOKEN", "raw-cargo-secret")
	t.Setenv("CRATES_TOKEN", "cargo-secret")
	var output bytes.Buffer
	if err := publishCargoPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(captured)), "cargo:cargo-secret\nversion:1.2.3"; got != want {
		t.Fatalf("publishCargoPackages() capture = %q, want %q", got, want)
	}
	for path, want := range map[string]string{
		filepath.Join(templateRoot, "Cargo.toml"): cargoManifest,
		lockPath: "original lock\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("restored %s = %q, want %q", path, got, want)
		}
	}
}
