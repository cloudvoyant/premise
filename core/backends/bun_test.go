package backends

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

// Fixtures -------------------------------------------------------------------

type unpublishedNPMTransport struct{ fallback http.RoundTripper }

// Tests ----------------------------------------------------------------------

func TestBunPluginSelectsRegistryPackagesIndependentlyOfVisibility(t *testing.T) {
	root := t.TempDir()
	for _, filename := range []string{"package.json", "bunfig.toml"} {
		if err := os.WriteFile(filepath.Join(root, filename), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, content   string
		public, publish bool
	}{
		{"public-cli", `{"name":"public-cli","private":false,"publishConfig":{"access":"public","registry":"https://registry.npmjs.org/"}}`, true, true},
		{"commander-template", `{"name":"premise-commander","private":false,"publishConfig":{"access":"public","registry":"https://registry.npmjs.org/"}}`, true, true},
		{"restricted-lib", `{"name":"restricted-lib","private":false,"publishConfig":{"access":"restricted","registry":"https://registry.example.com/"}}`, false, true},
		{"internal-app", `{"name":"internal-app","private":true,"publishConfig":{"access":"public","registry":"https://registry.npmjs.org/"}}`, false, false},
		{"static-site", `{"name":"static-site","private":true}`, false, false},
	}
	plugin := Bun{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			template := templateFixture(tc.name, "app")
			directory := filepath.Join(root, filepath.FromSlash(template.Path))
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			public, err := plugin.IsPackagePublic(root, template)
			if err != nil || public != tc.public {
				t.Fatalf("IsPackagePublic() = %v, %v", public, err)
			}
			publish, err := plugin.ShouldPublishPackage(root, template)
			if err != nil || publish != tc.publish {
				t.Fatalf("ShouldPublishPackage() = %v, %v", publish, err)
			}
		})
	}
	manifest := NewManifest("bun-fixture")
	manifest.Workspace.PackageManagers = []string{"bun"}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	config, err := plugin.CreateGoReleaserConfig(root, manifest)
	if err != nil || config != "" {
		t.Fatalf("Bun downloadable config = %q, %v", config, err)
	}
	var output bytes.Buffer
	registerBuiltins(t)
	if err := core.BuildReleaseArtifacts(t.Context(), root, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "no downloadable release artifacts") {
		t.Fatalf("Bun release output = %q", output.String())
	}
}

func TestBunPublicationPassesOnlyScopedCredentialsAndVersion(t *testing.T) {
	useUnpublishedNPMVersions(t)
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	template := templateFixture("public-cli", "app")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{template}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, filepath.FromSlash(template.Path))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(`{"name":"public-cli","private":false,"publishConfig":{"access":"public","registry":"https://registry.npmjs.org/"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "published")
	shim := `#!/bin/sh
set -eu
[ -z "${NODE_AUTH_TOKEN:-}" ]
[ -z "${GITHUB_TOKEN:-}" ]
case "$*" in
  "task info publish --json") [ -z "${NPM_CONFIG_USERCONFIG:-}" ] ;;
  "run publish")
    [ "$PREMISE_PUBLISH_VERSION" = "1.2.3" ]
    grep -F '//registry.npmjs.org/:_authToken=npm-secret' "$NPM_CONFIG_USERCONFIG" >/dev/null
    printf '%s' "$NPM_CONFIG_USERCONFIG" > "$CAPTURE"
    ;;
  *) exit 9 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("NODE_AUTH_TOKEN", "npm-secret")
	t.Setenv("GITHUB_TOKEN", "github-secret")
	var output bytes.Buffer
	if err := publishBunPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	configPath, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(configPath)); !os.IsNotExist(err) {
		t.Fatalf("temporary npm credential file still exists: %v", err)
	}
}

func TestBunPublicationSkipsPrivatePackagesWithoutCredentials(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	template := templateFixture("private-app", "app")
	manifest.TemplateRegistry = &TemplateRegistry{WorkspaceFiles: []string{}, Templates: []Template{template}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, filepath.FromSlash(template.Path))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(`{"name":"private-app","private":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := publishBunPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "skip: private-app Bun registry publication disabled\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestBunPublicationAggregatesPreflightErrors(t *testing.T) {
	useUnpublishedNPMVersions(t)
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	for _, name := range []string{"bad-json", "bad-registry", "missing-task", "ready"} {
		template := templateFixture(name, "app")
		manifest.TemplateRegistry = appendBunTemplate(manifest.TemplateRegistry, template)
		directory := filepath.Join(root, filepath.FromSlash(template.Path))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"name":"` + name + `","publishConfig":{"access":"public","registry":"https://registry.npmjs.org/"}}`
		switch name {
		case "bad-json":
			content = `{"name":`
		case "bad-registry":
			content = `{"name":"bad-registry","publishConfig":{"access":"public","registry":"http://registry.example.com/"}}`
		}
		if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := (Bun{}).ValidatePackage(root, templateFixture("bad-registry", "app")); err == nil || !strings.Contains(err.Error(), "invalid HTTPS registry") {
		t.Fatalf("ValidatePackage() = %v", err)
	}
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "published")
	shim := `#!/bin/sh
case "$*" in
  "task info publish --json")
    case "$PWD" in */missing-task) echo "Task not found" >&2; exit 1;; esac ;;
  "run publish") printf 'published' > "$CAPTURE" ;;
  *) exit 9 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("NODE_AUTH_TOKEN", "")
	ready, err := (Bun{}).WillPublishOk(t.Context(), root, templateFixture("ready", "app"), "v1.2.3", "publish")
	if err != nil || !ready {
		t.Fatalf("WillPublishOk(ready) = %v, %v", ready, err)
	}
	var output bytes.Buffer
	err = publishBunPackages(t.Context(), root, "v1.2.3", "publish", &output, &output)
	for _, want := range []string{"bad-json", "bad-registry", "missing-task", "NODE_AUTH_TOKEN"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error %v does not contain %q", err, want)
		}
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatalf("publication started despite preflight failures: %v", err)
	}
}

func TestBunVersionExistsEscapesScopedPackageOnce(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.EscapedPath()
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	registry, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = bunVersionExists(t.Context(), server.Client(), registry, "@scope/package", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/@scope%2Fpackage/1.2.3" {
		t.Fatalf("registry path = %q, want single-escaped scoped package", gotPath)
	}
}

func TestBunPublicationPreflightsAllTargetsBeforePublishing(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	events := filepath.Join(t.TempDir(), "events")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		file, err := os.OpenFile(events, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = file.WriteString("availability\n")
		_ = file.Close()
		if strings.HasSuffix(request.URL.Path, "/existing/1.2.3") {
			writer.WriteHeader(http.StatusOK)
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = originalTransport }()
	for _, name := range []string{"existing", "pending"} {
		template := templateFixture(name, "app")
		manifest.TemplateRegistry = appendBunTemplate(manifest.TemplateRegistry, template)
		directory := filepath.Join(root, filepath.FromSlash(template.Path))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"name":"` + name + `","publishConfig":{"registry":"` + server.URL + `/npm/"}}`
		if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	shim := `#!/bin/sh
set -eu
case "$*" in
  "task info publish --json") ;;
  "run publish") printf 'publish:%s\\n' "$PWD" >> "$EVENTS" ;;
  *) exit 9 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("EVENTS", events)
	t.Setenv("NODE_AUTH_TOKEN", "test-token")
	var output bytes.Buffer
	if err := publishBunPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(events)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(data))
	if len(lines) != 3 || lines[0] != "availability" || lines[1] != "availability" || !strings.HasPrefix(lines[2], "publish:") {
		t.Fatalf("events = %q, want both availability checks before one publish", lines)
	}
	if !strings.Contains(output.String(), "skip: existing 1.2.3 already published") {
		t.Fatalf("output = %q, want explicit existing-package skip", output.String())
	}
	if strings.Contains(output.String(), "existing 1.2.3\n") || !strings.Contains(output.String(), "pending 1.2.3\n") {
		t.Fatalf("output = %q, want only pending publication", output.String())
	}
}

func TestBunPublicationReusesCredentialsPerRegistry(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
	manifest.Workspace.Kind = core.ProjectKindTemplateRegistry
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = originalTransport }()
	for _, test := range []struct{ name, registry string }{
		{"first", server.URL + "/"},
		{"second", server.URL + "/"},
		{"other", server.URL + "/custom/"},
	} {
		template := templateFixture(test.name, "app")
		manifest.TemplateRegistry = appendBunTemplate(manifest.TemplateRegistry, template)
		directory := filepath.Join(root, filepath.FromSlash(template.Path))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"name":"` + test.name + `","publishConfig":{"registry":"` + test.registry + `"}}`
		if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "configs")
	shim := `#!/bin/sh
set -eu
case "$*" in
  "task info publish --json") [ -z "${NPM_CONFIG_USERCONFIG:-}" ] ;;
  "run publish")
    case "$PWD" in
      */other)
        grep -F '/custom/:_authToken=shared-token' "$NPM_CONFIG_USERCONFIG" >/dev/null ;;
      *)
        grep -F '_authToken=shared-token' "$NPM_CONFIG_USERCONFIG" >/dev/null
        ! grep -F '/custom/' "$NPM_CONFIG_USERCONFIG" >/dev/null ;;
    esac
    printf '%s\n' "$NPM_CONFIG_USERCONFIG" >> "$CAPTURE" ;;
  *) exit 9 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CAPTURE", capture)
	t.Setenv("NODE_AUTH_TOKEN", "shared-token")
	var output bytes.Buffer
	if err := publishBunPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	paths := strings.Fields(string(data))
	if len(paths) != 3 || paths[0] != paths[2] || paths[0] == paths[1] {
		t.Fatalf("credential files = %v; want one reused, one isolated", paths)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary credential file %q still exists: %v", path, err)
		}
	}
}

func TestBunGeneration(t *testing.T) {
	registerBuiltins(t)
	t.Run("when the project has no package.json", func(t *testing.T) {
		for _, existing := range []bool{false, true} {
			name := "when the workspace has no npm project"
			if existing {
				name = "when the workspace already has npm projects"
			}
			t.Run(name, func(t *testing.T) {
				// Arrange
				root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
				original := `{"name":"client","dependencies":{"tool":"1.2.0"}}`
				if existing {
					writePackageFixtureFile(t, root, "package.json", original)
				}
				writePackageFixtureFile(t, stage, "README.md", "generic project")
				writePackageFixtureFile(t, registry, "package.json", `{"dependencies":{"tool":"2.0.0"}}`)

				// Act
				plan, err := core.BuildGeneratePlan(core.GenerateParameters{
					PackageManagers:        []string{"bun"},
					RegistryRoot:           registry,
					RegistryWorkspaceFiles: []string{"package.json"},
					ClientRepoRoot:         root,
					TemplateRoot:           stage,
					ProjectPath:            "apps/tool",
					TemplateKind:           "app",
					ResolveConflict:        core.MergeDecisions{}.Resolve,
				})
				if err != nil {
					t.Fatal(err)
				}
				discardGenerationPlan(t, plan, root, "apps/tool")

				// Assert
				t.Run("it leaves npm roots alone because there is nothing native to merge", func(t *testing.T) {
					for _, entry := range plan.Entries {
						if entry.Path == "package.json" || entry.Path == "bun.lock" || entry.Path == "bun.lockb" {
							t.Fatalf("unexpected native output: %#v", entry)
						}
					}
					data, readErr := os.ReadFile(filepath.Join(root, "package.json"))
					if existing {
						if readErr != nil || string(data) != original {
							t.Fatalf("client changed: %s %v", data, readErr)
						}
					} else if !os.IsNotExist(readErr) {
						t.Fatalf("unexpected npm root: %s %v", data, readErr)
					}
				})
			})
		}
	})
	t.Run("when the project is the first npm project in the workspace", func(t *testing.T) {
		// Arrange
		root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
		writePackageFixtureFile(t, stage, "package.json", `{"name":"app","dependencies":{"project-only":"1.0.0"}}`)
		writePackageFixtureFile(t, registry, "package.json", `{"name":"registry","workspaces":["templates/*"],"devDependencies":{"tool":"1.2.0"}}`)

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{
			WorkspaceName:          "client",
			PackageManagers:        []string{"bun"},
			RegistryRoot:           registry,
			RegistryWorkspaceFiles: []string{"package.json"},
			ClientRepoRoot:         root,
			TemplateRoot:           stage,
			ProjectPath:            "apps/tool",
			TemplateKind:           "app",
			ResolveConflict:        core.MergeDecisions{}.Resolve,
		})
		if err != nil {
			t.Fatal(err)
		}
		discardGenerationPlan(t, plan, root, "apps/tool")

		// Assert
		t.Run("it creates client workspace membership and merges root tooling only", func(t *testing.T) {
			var output map[string]any
			data := generationOutput(t, plan, "package.json")
			if err := json.Unmarshal(data, &output); err != nil {
				t.Fatal(err)
			}
			if output["name"] != "client" || !strings.Contains(string(data), "apps/*") || !strings.Contains(string(data), "libs/*") || !strings.Contains(string(data), "1.2.0") {
				t.Fatalf("unexpected new root: %s", data)
			}
			if strings.Contains(string(data), "project-only") || strings.Contains(string(data), "templates/*") {
				t.Fatalf("registry or project identity leaked: %s", data)
			}
			if _, err := os.Stat(filepath.Join(root, "package.json")); !os.IsNotExist(err) {
				t.Fatalf("root published during planning: %v", err)
			}
		})
	})
	t.Run("when the project and workspace both have package.json", func(t *testing.T) {
		t.Run("when the registry adds only new dependencies and settings", func(t *testing.T) {
			// Arrange
			root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
			original, err := os.ReadFile("../testdata/package-generation/package.json")
			if err != nil {
				t.Fatal(err)
			}
			writePackageFixtureFile(t, root, "package.json", string(original))
			writePackageFixtureFile(t, registry, "package.json", `{"name":"registry-only","private":true,"workspaces":["templates/*"],"scripts":{"shared":"echo shared"},"devDependencies":{"new-tool":"^3.0.0"}}`)
			writePackageFixtureFile(t, stage, "package.json", `{"name":"tool","dependencies":{"project-only":"1.0.0"}}`)

			// Act
			plan, err := core.BuildGeneratePlan(core.GenerateParameters{
				WorkspaceName:          "client",
				PackageManagers:        []string{"bun"},
				RegistryRoot:           registry,
				RegistryWorkspaceFiles: []string{"package.json"},
				ClientRepoRoot:         root,
				TemplateRoot:           stage,
				ProjectPath:            "apps/tool",
				TemplateKind:           "app",
				ResolveConflict:        core.MergeDecisions{}.Resolve,
			})
			if err != nil {
				t.Fatal(err)
			}
			discardGenerationPlan(t, plan, root, "apps/tool")

			// Assert
			t.Run("it merges disjoint keys and preserves client identity and unknown fields", func(t *testing.T) {
				var result map[string]any
				if err := json.Unmarshal(generationOutput(t, plan, "package.json"), &result); err != nil {
					t.Fatal(err)
				}
				if result["name"] != "client-owned-root" || result["private"] != false || result["version"] != "7.0.0" || result["clientMetadata"] == nil {
					t.Fatalf("client identity lost: %#v", result)
				}
				output := string(generationOutput(t, plan, "package.json"))
				for _, expected := range []string{"existing-tool", "new-tool", "nohoist", "apps/tool", "echo client", "echo shared"} {
					if !strings.Contains(output, expected) {
						t.Fatalf("missing %q in %s", expected, output)
					}
				}
				if strings.Contains(output, "templates/") || strings.Contains(output, "project-only") {
					t.Fatalf("registry membership or project dependencies leaked into root: %s", output)
				}
				count := 0
				for _, entry := range plan.Entries {
					if entry.Path == "package.json" {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("native root was merged more than once: %#v", plan.Entries)
				}
			})
		})
		t.Run("when dependency ranges conflict", func(t *testing.T) {
			// Arrange
			root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
			writePackageFixtureFile(t, root, "package.json", `{"name":"client","devDependencies":{"tool":"^1"}}`)
			writePackageFixtureFile(t, registry, "package.json", `{"devDependencies":{"tool":"^2"}}`)
			writePackageFixtureFile(t, stage, "package.json", `{"name":"app"}`)

			_, err := core.BuildGeneratePlan(core.GenerateParameters{
				PackageManagers:        []string{"bun"},
				RegistryRoot:           registry,
				RegistryWorkspaceFiles: []string{"package.json"},
				ClientRepoRoot:         root,
				TemplateRoot:           stage,
				ProjectPath:            "apps/tool",
				TemplateKind:           "app",
				ResolveConflict:        core.MergeDecisions{}.Resolve,
			})

			// Assert
			t.Run("it reports a focused dependency conflict instead of replacing the root", func(t *testing.T) {
				if err == nil || !strings.Contains(err.Error(), "devDependencies.tool") {
					t.Fatalf("expected focused conflict: %v", err)
				}
				original, err := os.ReadFile(filepath.Join(root, "package.json"))
				if err != nil || !strings.Contains(string(original), `"tool":"^1"`) {
					t.Fatal("aborted merge changed the client")
				}
			})
		})
	})

	t.Run("when the project and workspace already have npm dependencies", func(t *testing.T) {
		for _, scenario := range []struct {
			name        string
			incoming    string
			wantVersion string
			wantError   bool
		}{
			{
				name:        "when there are no incoming dependencies",
				wantVersion: "1.2.0",
			},
			{
				name:        "when both versions match",
				incoming:    "1.2.0",
				wantVersion: "1.2.0",
			},
			{
				name:        "when the incoming minor version is newer",
				incoming:    "1.3.0",
				wantVersion: "1.3.0",
			},
			{
				name:        "when the incoming patch version is newer",
				incoming:    "1.2.1",
				wantVersion: "1.2.1",
			},
			{
				name:        "when the client minor version is newer",
				incoming:    "1.1.0",
				wantVersion: "1.2.0",
			},
			{
				name:      "when the incoming major version is higher",
				incoming:  "2.0.0",
				wantError: true,
			},
			{
				name:      "when the incoming major version is lower",
				incoming:  "0.9.0",
				wantError: true,
			},
		} {
			t.Run(scenario.name, func(t *testing.T) {
				// Arrange
				root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
				original := `{"name":"client","devDependencies":{"tool":"1.2.0"}}`
				shared := `{}`
				if scenario.incoming != "" {
					shared = `{"devDependencies":{"tool":"` + scenario.incoming + `"}}`
				}
				writePackageFixtureFile(t, root, "package.json", original)
				writePackageFixtureFile(t, registry, "package.json", shared)
				writePackageFixtureFile(t, stage, "package.json", `{"name":"app"}`)

				// Act
				plan, err := core.BuildGeneratePlan(core.GenerateParameters{
					PackageManagers:        []string{"bun"},
					RegistryRoot:           registry,
					RegistryWorkspaceFiles: []string{"package.json"},
					ClientRepoRoot:         root,
					TemplateRoot:           stage,
					ProjectPath:            "apps/tool",
					TemplateKind:           "app",
					ResolveConflict:        core.MergeDecisions{}.Resolve,
				})
				if plan != nil {
					discardGenerationPlan(t, plan, root, "apps/tool")
				}

				// Assert
				t.Run("it retains compatible dependencies and rejects major changes without changing the client", func(t *testing.T) {
					if scenario.wantError {
						if err == nil || !strings.Contains(err.Error(), "incompatible major versions") {
							t.Fatalf("expected major-version conflict, got %v", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						var output struct {
							DevDependencies map[string]string `json:"devDependencies"`
						}
						if err := json.Unmarshal(generationOutput(t, plan, "package.json"), &output); err != nil {
							t.Fatal(err)
						}
						if output.DevDependencies["tool"] != scenario.wantVersion {
							t.Fatalf("dependencies = %v, want tool %s", output.DevDependencies, scenario.wantVersion)
						}
					}
					unchanged, readErr := os.ReadFile(filepath.Join(root, "package.json"))
					if readErr != nil || string(unchanged) != original {
						t.Fatalf("client changed: %s %v", unchanged, readErr)
					}
				})
			})
		}
	})

	t.Run("when existing npm settings conflict", func(t *testing.T) {
		// Arrange
		root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
		writePackageFixtureFile(t, root, "package.json", `{"name":"client","scripts":{"build":"client build"},"files":["common","client"]}`)
		writePackageFixtureFile(t, registry, "package.json", `{"scripts":{"build":"shared build"},"files":["shared","common"]}`)
		writePackageFixtureFile(t, stage, "package.json", `{"name":"app"}`)

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{
			PackageManagers:        []string{"bun"},
			RegistryRoot:           registry,
			RegistryWorkspaceFiles: []string{"package.json"},
			ClientRepoRoot:         root,
			TemplateRoot:           stage,
			ProjectPath:            "apps/tool",
			TemplateKind:           "app",
			ResolveConflict:        core.MergeDecisions{"scripts.build": {Choice: core.MergeChoiceUseSelected}}.Resolve,
		})
		if err != nil {
			t.Fatal(err)
		}
		discardGenerationPlan(t, plan, root, "apps/tool")

		// Assert
		t.Run("it uses the common document policy for settings and the caller's field decision", func(t *testing.T) {
			var output struct {
				Scripts map[string]string `json:"scripts"`
				Files   []string          `json:"files"`
			}
			if err := json.Unmarshal(generationOutput(t, plan, "package.json"), &output); err != nil {
				t.Fatal(err)
			}
			if output.Scripts["build"] != "client build" || strings.Join(output.Files, ",") != "shared,common,client" {
				t.Fatalf("unexpected merged settings: %#v", output)
			}
		})
	})

	t.Run("when the native root excludes the selected project", func(t *testing.T) {
		// Arrange
		root, stage := t.TempDir(), t.TempDir()
		writePackageFixtureFile(t, root, "package.json", `{"workspaces":["apps/*","!apps/tool"]}`)
		writePackageFixtureFile(t, stage, "package.json", `{"name":"tool"}`)

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{
			PackageManagers: []string{"bun"},
			ClientRepoRoot:  root,
			TemplateRoot:    stage,
			ProjectPath:     "apps/tool",
			TemplateKind:    "app",
		})

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
}

func TestBunDependencyManifests(t *testing.T) {
	t.Run("when parsing and regenerating package.json", func(t *testing.T) {
		// Arrange
		manifest := readPackageManifestFixture(t, "package.json")

		// Act
		dependencies, err := (Bun{}).ParseDependencies(manifest)
		if err != nil {
			t.Fatal(err)
		}
		regenerated, err := (Bun{}).WriteDependencies(manifest, dependencies)

		// Assert
		t.Run("it keeps runtime and development dependencies in separate scopes", func(t *testing.T) {
			versions := map[string]string{}
			for _, dependency := range dependencies {
				versions[strings.Join(dependency.Scope, ".")+"."+dependency.Name] = dependency.Version
			}
			want := map[string]string{"dependencies.tool": "1.2.0", "dependencies.unchanged": "1.0.0", "devDependencies.tool": "9.0.0"}
			if !reflect.DeepEqual(versions, want) {
				t.Fatalf("parsed dependencies = %#v", versions)
			}
		})
		t.Run("it regenerates native dependency sections and preserves client settings", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			var original, roundTrip map[string]any
			if err := json.Unmarshal(manifest, &original); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(regenerated, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, roundTrip) {
				t.Fatalf("native round trip lost fields:\n%s", regenerated)
			}
		})
	})

	for _, scenario := range []struct {
		name, incoming, dependency, wantVersion, wantError string
		choice                                             core.MergeChoice
	}{
		{name: "when the registry adds a dependency", incoming: `{"dependencies":{"added":"1.0.0"}}`, dependency: "added", wantVersion: "1.0.0"},
		{name: "when both manifests select the same version", incoming: `{"dependencies":{"tool":"1.2.0"}}`, dependency: "tool", wantVersion: "1.2.0"},
		{name: "when the registry selects a newer compatible version", incoming: `{"dependencies":{"tool":"1.3.0"}}`, dependency: "tool", wantVersion: "1.3.0"},
		{name: "when the client already selects a newer version", incoming: `{"dependencies":{"tool":"1.1.0"}}`, dependency: "tool", wantVersion: "1.2.0"},
		{name: "when the manifests select different majors", incoming: `{"dependencies":{"tool":"2.0.0"}}`, wantError: "incompatible major versions"},
		{name: "when a version range needs an explicit choice", incoming: `{"dependencies":{"tool":"^1.4.0"}}`, dependency: "tool", wantVersion: "^1.4.0", choice: core.MergeChoiceKeepShared},
		{name: "when a local path needs an explicit choice", incoming: `{"dependencies":{"tool":"file:../helper"}}`, dependency: "tool", wantVersion: "1.2.0", choice: core.MergeChoiceUseSelected},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			client := readPackageManifestFixture(t, "package.json")
			decisions := core.MergeDecisions{"dependencies.tool": {Choice: scenario.choice}}

			// Act
			output, err := core.MergeManifestDependencies(Bun{}, "package.json", []byte(scenario.incoming), client, decisions.Resolve)

			// Assert
			if scenario.wantError != "" {
				t.Run("it reports the dependency conflict and leaves the source unchanged", func(t *testing.T) {
					if err == nil || !strings.Contains(err.Error(), scenario.wantError) || output != nil || !bytes.Equal(client, readPackageManifestFixture(t, "package.json")) {
						t.Fatalf("merge error = %v, output = %s", err, output)
					}
				})
				return
			}
			t.Run("it writes merged dependencies while preserving scopes and client identity", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				var pkg map[string]any
				if err := json.Unmarshal(output, &pkg); err != nil {
					t.Fatal(err)
				}
				runtime := pkg["dependencies"].(map[string]any)
				development := pkg["devDependencies"].(map[string]any)
				if runtime[scenario.dependency] != scenario.wantVersion || development["tool"] != "9.0.0" || pkg["name"] != "client-owned" || pkg["clientMetadata"] == nil || pkg["scripts"] == nil {
					t.Fatalf("wrong merged package:\n%s", output)
				}
			})
		})
	}
	t.Run("when an incoming package.json has invalid dependency syntax", func(t *testing.T) {
		// Arrange
		client := readPackageManifestFixture(t, "package.json")
		incoming := []byte("{\"dependencies\":{\"tool\":42}}")

		// Act
		output, err := core.MergeManifestDependencies(Bun{}, "package.json", incoming, client, core.MergeDecisions{}.Resolve)

		// Assert
		t.Run("it rejects parsing before producing a merged manifest", func(t *testing.T) {
			if err == nil || !strings.Contains(err.Error(), "parse shared") || output != nil || !bytes.Equal(client, readPackageManifestFixture(t, "package.json")) {
				t.Fatalf("invalid dependency syntax was accepted: %s, %v", output, err)
			}
		})
	})

}

// Private helpers ------------------------------------------------------------

func appendBunTemplate(registry *TemplateRegistry, template Template) *TemplateRegistry {
	if registry == nil {
		registry = &TemplateRegistry{WorkspaceFiles: []string{}}
	}
	registry.Templates = append(registry.Templates, template)
	return registry
}

func (transport unpublishedNPMTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Hostname() == "registry.npmjs.org" {
		return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody, Header: make(http.Header), Request: request}, nil
	}
	return transport.fallback.RoundTrip(request)
}

func useUnpublishedNPMVersions(t *testing.T) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = unpublishedNPMTransport{fallback: previous}
	t.Cleanup(func() { http.DefaultTransport = previous })
}
