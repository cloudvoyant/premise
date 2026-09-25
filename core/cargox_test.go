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
	if registry, err := isCargoRegistry(root); err != nil || registry {
		t.Fatalf("isCargoRegistry(templates/Cargo.toml) = %v, %v; want false", registry, err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if registry, err := isCargoRegistry(root); err != nil || !registry {
		t.Fatalf("isCargoRegistry(Cargo.toml) = %v, %v; want true", registry, err)
	}
}

func TestInspectCargoTemplatePackage(t *testing.T) {
	for _, test := range []struct {
		name            string
		kind            string
		manifest        string
		nestedManifest  string
		found           bool
		registryPublish bool
		wantError       string
	}{
		{name: "default-package", kind: "lib", manifest: "[package]\nname = \"default-package\"\nversion = \"0.1.0\"\n", found: true, registryPublish: true},
		{name: "internal-package", kind: "lib", manifest: "[package]\nname = \"internal-package\"\nversion = \"0.1.0\"\npublish = false # internal\n", found: true},
		{name: "private-registry", kind: "lib", manifest: "[package]\nname = 'private-registry'\nversion = '0.1.0'\npublish = ['private']\n", found: true},
		{name: "crates-allowlist", kind: "lib", manifest: "[package]\nname = 'crates-allowlist'\nversion = '0.1.0'\npublish = ['crates-io']\n", found: true, registryPublish: true},
		{name: "direct-app", kind: "app", manifest: "[package]\nname = \"direct-app\"\nversion = \"0.1.0\"\n", found: true, registryPublish: true},
		{name: "nested-app", kind: "app", nestedManifest: "[package]\nname = \"nested-app\"\nversion = \"0.1.0\"\n"},
		{name: "virtual-workspace", kind: "app", manifest: "[workspace]\nmembers = [\"src-tauri\"]\n"},
		{name: "other-section", kind: "app", manifest: "[workspace]\nname = 'other-section'\nversion = '0.1.0'\n[package]\nname = 'other-section'\nversion = '0.1.0'\n[dependencies]\npublish = false\n", found: true, registryPublish: true},
		{name: "mismatched", kind: "lib", manifest: "[package]\nname = \"other\"\nversion = \"0.1.0\"\n", wantError: `does not match declared template "mismatched"`},
		{name: "malformed", kind: "lib", manifest: "[package]\nname = \"malformed\"\n", wantError: "has no [package] version"},
		{name: "invalid-toml", kind: "lib", manifest: "[package]\nname = 'invalid-toml'\nversion = [\n", wantError: "parse Cargo manifest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			template := templateFixture(test.name, test.kind)
			directory := filepath.Join(root, filepath.FromSlash(template.Path))
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			if test.manifest != "" {
				if err := os.WriteFile(filepath.Join(directory, "Cargo.toml"), []byte(test.manifest), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if test.nestedManifest != "" {
				nested := filepath.Join(directory, "src-tauri")
				if err := os.MkdirAll(nested, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(nested, "Cargo.toml"), []byte(test.nestedManifest), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, found, err := inspectCargoTemplatePackage(root, template)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("inspectCargoTemplatePackage() error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if found != test.found {
				t.Fatalf("inspectCargoTemplatePackage() found = %v, want %v", found, test.found)
			}
			if !found {
				return
			}
			if got.Template.Name != template.Name || got.Directory != directory || got.Name != template.Name {
				t.Fatalf("inspectCargoTemplatePackage() = %#v", got)
			}
			if got.RegistryPublish != test.registryPublish {
				t.Fatalf("inspectCargoTemplatePackage() RegistryPublish = %v, want %v", got.RegistryPublish, test.registryPublish)
			}
		})
	}
}

func TestSetCargoPackageVersionPreservesUnrelatedSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cargo.toml")
	original := "[workspace.package]\nversion = '0.1.0'\n[package] # application\nname = 'example'\nversion = '0.2.0' # current\n[dependencies]\nversion = 'keep'\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setCargoPackageVersion(path, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(original, "version = '0.2.0' # current", `version = "1.2.3" # current`, 1)
	if string(got) != want {
		t.Fatalf("setCargoPackageVersion() = %q, want %q", got, want)
	}
}

func TestCargoPublicationPreflightsEveryPackageBeforePublishing(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{
		templateFixture("a-first", "lib"),
		templateFixture("z-second", "lib"),
	}}
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

func TestCargoPublicationSkipsIneligiblePackagesWithoutCredentialsOrTasks(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{
		templateFixture("nested-app", "app"),
		templateFixture("internal-lib", "lib"),
	}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	nestedRoot := filepath.Join(root, "templates", "nested-app", "src-tauri")
	if err := os.MkdirAll(nestedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedRoot, "Cargo.toml"), []byte("[package]\nname = \"nested-app\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	internalRoot := filepath.Join(root, "templates", "internal-lib")
	if err := os.MkdirAll(internalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	internalManifest := "[package]\nname = \"internal-lib\"\nversion = \"0.1.0\"\npublish = false\n"
	if err := os.WriteFile(filepath.Join(internalRoot, "Cargo.toml"), []byte(internalManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, "Cargo.lock")
	if err := os.WriteFile(lockPath, []byte("original lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "mise-ran")
	shim := "#!/bin/sh\nprintf 'unexpected mise invocation' > \"$CAPTURE\"\nexit 99\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	var output bytes.Buffer
	if err := publishCargoPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	wantOutput := "skip: nested-app has no direct Cargo package\nskip: internal-lib Cargo registry publication disabled\n"
	if output.String() != wantOutput {
		t.Fatalf("publishCargoPackages() output = %q, want %q", output.String(), wantOutput)
	}
	if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mise ran for skipped packages: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(internalRoot, "Cargo.toml"): internalManifest,
		lockPath: "original lock\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("untouched %s = %q, want %q", path, got, want)
		}
	}
}

func TestCargoPublicationPublishesOnlyEligiblePackages(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{
		templateFixture("nested-app", "app"),
		templateFixture("internal-lib", "lib"),
		templateFixture("public-lib", "lib"),
	}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	nestedRoot := filepath.Join(root, "templates", "nested-app", "src-tauri")
	if err := os.MkdirAll(nestedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedRoot, "Cargo.toml"), []byte("[package]\nname = \"nested-app\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	internalRoot := filepath.Join(root, "templates", "internal-lib")
	publicRoot := filepath.Join(root, "templates", "public-lib")
	for _, directory := range []string{internalRoot, publicRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	internalManifest := "[package]\nname = \"internal-lib\"\nversion = \"0.1.0\"\npublish = false\n"
	publicManifest := "[package]\nname = \"public-lib\"\nversion = \"0.1.0\"\n"
	if err := os.WriteFile(filepath.Join(internalRoot, "Cargo.toml"), []byte(internalManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publicRoot, "Cargo.toml"), []byte(publicManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\nmembers = [\"templates/public-lib\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, "Cargo.lock")
	if err := os.WriteFile(lockPath, []byte("original lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/crates/public-lib/1.2.3" {
			t.Errorf("unexpected crates.io request %s", request.URL.Path)
		}
		response.WriteHeader(http.StatusNotFound)
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
    printf 'preflight:%s\n' "$PWD" >> "$CAPTURE"
    ;;
  "exec -- cargo generate-lockfile")
    [ -z "${CRATES_TOKEN:-}" ]
    printf 'generated lock\n' > Cargo.lock
    ;;
  "run publish")
    [ -z "${CRATES_TOKEN:-}" ]
    printf 'publish:%s:%s:%s\n' "$PWD" "${CARGO_REGISTRY_TOKEN:-}" "${RELEASE_VERSION:-}" >> "$CAPTURE"
    ;;
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
	if err := publishCargoPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"skip: nested-app has no direct Cargo package",
		"skip: internal-lib Cargo registry publication disabled",
		"publish: public-lib 1.2.3",
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("publishCargoPackages() output does not contain %q: %q", want, output.String())
		}
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPublicRoot, err := filepath.EvalSymlinks(publicRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolvedInternalRoot, err := filepath.EvalSymlinks(internalRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolvedNestedRoot, err := filepath.EvalSymlinks(nestedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(captured); !strings.Contains(got, "preflight:"+resolvedPublicRoot) || !strings.Contains(got, "publish:"+resolvedPublicRoot+":cargo-secret:1.2.3") {
		t.Fatalf("eligible package capture = %q", got)
	}
	if strings.Contains(string(captured), resolvedInternalRoot) || strings.Contains(string(captured), resolvedNestedRoot) {
		t.Fatalf("skipped package ran a task: %q", captured)
	}
	for path, want := range map[string]string{
		filepath.Join(internalRoot, "Cargo.toml"): internalManifest,
		filepath.Join(publicRoot, "Cargo.toml"):   publicManifest,
		lockPath:                                  "original lock\n",
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

func TestPublishUsesCargoCredentialsAndRestoresVersions(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{templateFixture("example-crate", "lib")}}
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
