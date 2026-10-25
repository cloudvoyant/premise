package backends

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
	"github.com/pelletier/go-toml/v2"
)

// Tests ----------------------------------------------------------------------

func TestCargoGetPackageMetadata(t *testing.T) {
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

			got, found, err := (Cargo{}).GetPackageMetadata(root, template)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("GetPackageMetadata() error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if found != test.found {
				t.Fatalf("GetPackageMetadata() found = %v, want %v", found, test.found)
			}
			if !found {
				return
			}
			if got.Name != template.Name || got.Path != directory || got.PackageManager != "cargo" || got.Ecosystem != "cargo" {
				t.Fatalf("GetPackageMetadata() = %#v", got)
			}
			if got.Publishable != test.registryPublish || got.Public != test.registryPublish {
				t.Fatalf("GetPackageMetadata() publishable/public = %v/%v, want %v", got.Publishable, got.Public, test.registryPublish)
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
	if err := core.SetCargoPackageVersion(path, "1.2.3"); err != nil {
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
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
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

func TestCargoPublicationAggregatesPreflightErrorsBeforeChangingFiles(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	for _, name := range []string{"ready", "bad-name", "missing-task"} {
		template := templateFixture(name, "lib")
		if manifest.TemplateRegistry == nil {
			manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}}
		}
		manifest.TemplateRegistry.Templates = append(manifest.TemplateRegistry.Templates, template)
		directory := filepath.Join(root, filepath.FromSlash(template.Path))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		packageName := name
		if name == "bad-name" {
			packageName = "unexpected-name"
		}
		content := "[package]\nname = \"" + packageName + "\"\nversion = \"0.1.0\"\n"
		if err := os.WriteFile(filepath.Join(directory, "Cargo.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, "Cargo.lock")
	if err := os.WriteFile(lockPath, []byte("original lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "missing-task") {
			response.WriteHeader(http.StatusInternalServerError)
		} else {
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	originalAPI := cratesAPIBaseURL
	cratesAPIBaseURL = server.URL
	defer func() { cratesAPIBaseURL = originalAPI }()
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "side-effect")
	shim := `#!/bin/sh
case "$*" in
  "task info publish --json")
    case "$PWD" in */missing-task) echo "Task not found" >&2; exit 1;; esac ;;
  "exec -- cargo generate-lockfile"|"run publish") printf 'ran' > "$CAPTURE" ;;
  *) exit 9 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("CRATES_TOKEN", "")
	if err := (Cargo{}).ValidatePackage(root, templateFixture("bad-name", "lib")); err == nil || !strings.Contains(err.Error(), "does not match declared template") {
		t.Fatalf("ValidatePackage() = %v", err)
	}
	ready, err := (Cargo{}).WillPublishOk(t.Context(), root, templateFixture("ready", "lib"), "v1.2.3", "publish")
	if err != nil || !ready {
		t.Fatalf("WillPublishOk(ready) = %v, %v", ready, err)
	}
	var output bytes.Buffer
	err = publishCargoPackages(t.Context(), root, "v1.2.3", "publish", &output, &output)
	for _, want := range []string{"bad-name", "missing-task", "HTTP 500", "CRATES_TOKEN"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error %v does not contain %q", err, want)
		}
	}
	if _, err := os.Stat(capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("publication or lockfile generation started despite preflight failures: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "templates", "ready", "Cargo.toml")); err != nil || !strings.Contains(string(data), `version = "0.1.0"`) {
		t.Fatalf("Cargo version changed during preflight: %q, %v", data, err)
	}
	if data, err := os.ReadFile(lockPath); err != nil || string(data) != "original lock\n" {
		t.Fatalf("Cargo lockfile changed during preflight: %q, %v", data, err)
	}
}

func TestCargoPublicationSkipsIneligiblePackagesWithoutCredentialsOrTasks(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
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
	wantOutput := "skip: internal-lib Cargo registry publication disabled\nskip: nested-app has no direct Cargo package\n"
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
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
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
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
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

func TestCargoReleaseArtifactsExcludeDirectTauriApps(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("cargo-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	publicCLI := templateFixture("public-cli", "app")
	privateCLI := templateFixture("private-cli", "app")
	tauri := templateFixture("tauri-app", "app")
	manifest.TemplateRegistry = &TemplateRegistry{
		WorkspaceFiles: []string{},
		Templates:      []Template{publicCLI, privateCLI, tauri},
	}
	for _, template := range manifest.TemplateRegistry.Templates {
		path := filepath.Join(root, filepath.FromSlash(template.Path))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		cargo := "[package]\nname = \"" + template.Name + "\"\nversion = \"0.1.0\"\n"
		if template.Name != "public-cli" {
			cargo += "publish = false\n"
		}
		if err := os.WriteFile(filepath.Join(path, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(tauri.Path), "tauri.conf.json"), []byte(`{"productName":"tauri-app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ManifestFilename)
	if err := SaveManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := core.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := (Cargo{}).CreateGoReleaserConfig(root, loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"public-cli", "private-cli"} {
		if !strings.Contains(config, "binary: "+name) || !strings.Contains(config, "- id: "+name) {
			t.Fatalf("missing app %s from release configuration: %s", name, config)
		}
	}
	if strings.Contains(config, "tauri-app") {
		t.Fatalf("Tauri app was included in generic archives: %s", config)
	}
	if got := strings.Count(config, "x86_64-unknown-linux-gnu"); got != 2 {
		t.Fatalf("Linux x86-64 target count = %d, want 2", got)
	}
}

func TestCargoGeneration(t *testing.T) {
	registerBuiltins(t)
	t.Run("when extending an existing Cargo workspace", func(t *testing.T) {
		// Arrange
		root, stage := t.TempDir(), t.TempDir()
		original, err := os.ReadFile("../testdata/package-generation/Cargo.toml")
		if err != nil {
			t.Fatal(err)
		}
		writePackageFixtureFile(t, root, "Cargo.toml", string(original))
		writePackageFixtureFile(t, stage, "Cargo.toml", "[package]\nname = 'tool'\nversion = '0.1.0'\nedition = '2024'\n")

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"cargo"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app"})
		if err != nil {
			t.Fatal(err)
		}
		discardGenerationPlan(t, plan, root, "apps/tool")

		// Assert
		t.Run("it preserves client configuration outside membership", func(t *testing.T) {
			output := generationOutput(t, plan, "Cargo.toml")
			var result struct {
				Workspace struct {
					Members  []string `toml:"members"`
					Resolver string   `toml:"resolver"`
				} `toml:"workspace"`
			}
			if err := toml.Unmarshal(output, &result); err != nil {
				t.Fatal(err)
			}
			if strings.Join(result.Workspace.Members, ",") != "libs/existing,apps/tool" || result.Workspace.Resolver != "2" {
				t.Fatalf("workspace configuration = %#v", result)
			}
			for _, expected := range []string{"# Client root identity stays here.", "# Preserve this comment", "default-members = [\"libs/existing\"]", "lto = true # Keep profile comments.", "[workspace.dependencies]", "[workspace.metadata.client]"} {
				if !strings.Contains(string(output), expected) {
					t.Fatalf("client content lost: %s\n%s", expected, output)
				}
			}
			unchanged, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
			if err != nil || string(unchanged) != string(original) {
				t.Fatal("planning changed the client root")
			}
		})
	})
	t.Run("when Cargo membership uses another native layout", func(t *testing.T) {
		for _, scenario := range []struct {
			name     string
			manifest string
		}{
			{"when the workspace has no members list", "[workspace]\nresolver = '2'\n[workspace.package]\nlicense = 'MIT'\n"},
			{"when members use dotted keys", "workspace.members = ['libs/existing']\n"},
			{"when the workspace only declares package defaults", "[workspace.package]\nlicense = 'MIT'\n"},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				// Arrange
				root, stage := t.TempDir(), t.TempDir()
				writePackageFixtureFile(t, root, "Cargo.toml", scenario.manifest)
				writePackageFixtureFile(t, stage, "Cargo.toml", "[package]\nname='tool'\nversion='0.1.0'\nedition='2024'\n")

				// Act
				plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"cargo"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app"})
				if err != nil {
					t.Fatal(err)
				}
				discardGenerationPlan(t, plan, root, "apps/tool")

				// Assert
				t.Run("it adds the generated crate to the workspace", func(t *testing.T) {
					var result struct {
						Workspace struct {
							Members []string `toml:"members"`
						} `toml:"workspace"`
					}
					if err := toml.Unmarshal(generationOutput(t, plan, "Cargo.toml"), &result); err != nil {
						t.Fatal(err)
					}
					if len(result.Workspace.Members) == 0 || result.Workspace.Members[len(result.Workspace.Members)-1] != "apps/tool" {
						t.Fatalf("member not added: %#v", result)
					}
				})
			})
		}
	})

	t.Run("when the native root excludes the selected project", func(t *testing.T) {
		// Arrange
		root, stage := t.TempDir(), t.TempDir()
		writePackageFixtureFile(t, root, "Cargo.toml", "[workspace]\nmembers=[]\nexclude=['apps/*']\n")
		writePackageFixtureFile(t, stage, "Cargo.toml", "[package]\nname='tool'\nversion='0.1.0'\n")

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"cargo"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app"})

		// Assert
		t.Run("it rejects membership before publication", func(t *testing.T) {
			if plan != nil {
				discardGenerationPlan(t, plan, root, "apps/tool")
			}
			if err == nil || !strings.Contains(err.Error(), "excludes") {
				t.Fatalf("expected native exclusion error, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "apps/tool")); !os.IsNotExist(err) {
				t.Fatalf("destination exists: %v", err)
			}
		})
	})

	t.Run("when a registry contributes workspace dependencies", func(t *testing.T) {
		// Arrange
		root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
		original, err := os.ReadFile("../testdata/package-generation/Cargo.toml")
		if err != nil {
			t.Fatal(err)
		}
		writePackageFixtureFile(t, root, "Cargo.toml", string(original))
		writePackageFixtureFile(t, stage, "Cargo.toml", "[package]\nname='tool'\nversion='0.1.0'\nedition='2024'\n")
		writePackageFixtureFile(t, registry, "Cargo.toml", "[package]\nname='registry-development'\nversion='9.0.0'\n[workspace]\nmembers=['templates/*']\n[workspace.dependencies]\nshared='1.2.0'\n")

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"cargo"}, RegistryRoot: registry, RegistryWorkspaceFiles: []string{"Cargo.toml"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "apps/tool", TemplateKind: "app"})
		if err != nil {
			t.Fatal(err)
		}
		discardGenerationPlan(t, plan, root, "apps/tool")

		// Assert
		t.Run("it combines dependencies through generation without importing registry identity", func(t *testing.T) {
			var manifest struct {
				Package   struct{ Name string } `toml:"package"`
				Workspace struct {
					Members      []string       `toml:"members"`
					Dependencies map[string]any `toml:"dependencies"`
				} `toml:"workspace"`
			}
			output := generationOutput(t, plan, "Cargo.toml")
			if err := toml.Unmarshal(output, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Package.Name != "client-root" || strings.Join(manifest.Workspace.Members, ",") != "libs/existing,apps/tool" || manifest.Workspace.Dependencies["shared"] != "1.2.0" || manifest.Workspace.Dependencies["existing"] == nil || strings.Contains(string(output), "templates/*") {
				t.Fatalf("wrong generated Cargo workspace:\n%s", output)
			}
			unchanged, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
			if err != nil || !bytes.Equal(unchanged, original) {
				t.Fatal("planning mutated the client manifest")
			}
		})
	})

}

func TestCargoDependencyManifests(t *testing.T) {
	t.Run("when parsing and regenerating a Cargo manifest", func(t *testing.T) {
		// Arrange
		manifest := readPackageManifestFixture(t, "Cargo.toml")

		// Act
		dependencies, err := (Cargo{}).ParseDependencies(manifest)
		if err != nil {
			t.Fatal(err)
		}
		regenerated, err := (Cargo{}).WriteDependencies(manifest, dependencies)

		// Assert
		t.Run("it exposes dependency scopes, versions, paths, and feature settings", func(t *testing.T) {
			if len(dependencies) != 6 {
				t.Fatalf("dependencies = %#v", dependencies)
			}
			byScope := map[string]core.Dependency{}
			for _, dependency := range dependencies {
				byScope[strings.Join(dependency.Scope, ".")+"."+dependency.Name] = dependency
			}
			local := byScope["dependencies.local"]
			features := byScope["dependencies.unchanged"].Options.(map[string]any)
			if local.Version != "" || local.Options.(map[string]any)["path"] != "../helper" || features["default-features"] != false || !reflect.DeepEqual(features["features"], []any{"derive"}) || byScope["dev-dependencies.tool"].Version != "9.0.0" || byScope["workspace.dependencies.shared"].Version != "1.0.0" || byScope["target.cfg(unix).build-dependencies.compiler"].Version != "1.0.0" {
				t.Fatalf("native dependency information was lost: %#v", byScope)
			}
		})
		t.Run("it regenerates the manifest without changing native values", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			var original, roundTrip map[string]any
			if err := toml.Unmarshal(manifest, &original); err != nil {
				t.Fatal(err)
			}
			if err := toml.Unmarshal(regenerated, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, roundTrip) || !bytes.Equal(manifest, regenerated) {
				t.Fatalf("unchanged dependency round trip lost source content:\n%s", regenerated)
			}
		})
	})

	for _, scenario := range []struct {
		name, incoming, dependency, wantVersion, wantError string
		choice                                             core.MergeChoice
		scope                                              []string
	}{
		{name: "when the manifest adds a crate", incoming: "[dependencies]\nadded='1.0.0'", dependency: "added", wantVersion: "1.0.0"},
		{name: "when both manifests require the same version", incoming: "[dependencies]\ntool='1.2.0'", dependency: "tool", wantVersion: "1.2.0"},
		{name: "when the incoming manifest requires a newer minor", incoming: "[dependencies]\ntool='1.3.0'", dependency: "tool", wantVersion: "1.3.0"},
		{name: "when the client already requires a newer version", incoming: "[dependencies]\ntool='1.1.0'", dependency: "tool", wantVersion: "1.2.0"},
		{name: "when crate majors conflict", incoming: "[dependencies]\ntool='2.0.0'", wantError: "incompatible major versions"},
		{name: "when a range needs an explicit decision", incoming: "[dependencies]\ntool='~1.4'", dependency: "tool", wantVersion: "~1.4", choice: core.MergeChoiceKeepShared},
		{name: "when local paths conflict", incoming: "[dependencies]\nlocal={path='../other'}", dependency: "local", choice: core.MergeChoiceUseSelected},
		{name: "when workspace dependencies need an upgrade", incoming: "[workspace.dependencies]\nshared='1.2.0'", dependency: "shared", wantVersion: "1.2.0", scope: []string{"workspace", "dependencies"}},
		{name: "when target dependencies need an upgrade", incoming: "[target.'cfg(unix)'.build-dependencies]\ncompiler={version='1.1.0',optional=true}", dependency: "compiler", wantVersion: "1.1.0", scope: []string{"target", "cfg(unix)", "build-dependencies"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			client := readPackageManifestFixture(t, "Cargo.toml")
			decisions := core.MergeDecisions{"dependencies." + scenario.dependency: {Choice: scenario.choice}}

			// Act
			output, err := core.MergeManifestDependencies(Cargo{}, "Cargo.toml", []byte(scenario.incoming), client, decisions.Resolve)

			// Assert
			if scenario.wantError != "" {
				t.Run("it reports the conflict and leaves the source unchanged", func(t *testing.T) {
					if err == nil || !strings.Contains(err.Error(), scenario.wantError) || output != nil || !bytes.Equal(client, readPackageManifestFixture(t, "Cargo.toml")) {
						t.Fatalf("merge error = %v, output = %s", err, output)
					}
				})
				return
			}
			t.Run("it writes merged native dependencies and preserves unrelated Cargo settings", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				var manifest map[string]any
				if err := toml.Unmarshal(output, &manifest); err != nil {
					t.Fatal(err)
				}
				scope := scenario.scope
				if len(scope) == 0 {
					scope = []string{"dependencies"}
				}
				table := manifest
				for _, name := range scope {
					table = table[name].(map[string]any)
				}
				value := table[scenario.dependency]
				version, _ := value.(string)
				if options, ok := value.(map[string]any); ok {
					version, _ = options["version"].(string)
					if scenario.dependency == "local" && options["path"] != "../helper" {
						t.Fatalf("client path was replaced: %#v", options)
					}
				}
				pkg := manifest["package"].(map[string]any)
				profile := manifest["profile"].(map[string]any)["release"].(map[string]any)
				features := manifest["dependencies"].(map[string]any)["unchanged"].(map[string]any)
				if version != scenario.wantVersion || pkg["name"] != "client-owned" || pkg["version"] != "7.0.0" || pkg["metadata"] == nil || profile["lto"] != true || features["default-features"] != false {
					t.Fatalf("wrong merged Cargo manifest:\n%s", output)
				}
			})
		})
	}
	t.Run("when an incoming Cargo.toml has invalid dependency syntax", func(t *testing.T) {
		// Arrange
		client := readPackageManifestFixture(t, "Cargo.toml")
		incoming := []byte("[dependencies]\ntool={version=42}")

		// Act
		output, err := core.MergeManifestDependencies(Cargo{}, "Cargo.toml", incoming, client, core.MergeDecisions{}.Resolve)

		// Assert
		t.Run("it rejects parsing before producing a merged manifest", func(t *testing.T) {
			if err == nil || !strings.Contains(err.Error(), "parse shared") || output != nil || !bytes.Equal(client, readPackageManifestFixture(t, "Cargo.toml")) {
				t.Fatalf("invalid dependency syntax was accepted: %s, %v", output, err)
			}
		})
	})

}
