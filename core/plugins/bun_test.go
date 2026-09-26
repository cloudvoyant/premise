package plugins

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

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
			public, err := plugin.IsPublic(root, template)
			if err != nil || public != tc.public {
				t.Fatalf("IsPublic() = %v, %v", public, err)
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
	if err := core.BuildReleaseSnapshot(t.Context(), root, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "no downloadable release artifacts") {
		t.Fatalf("Bun release output = %q", output.String())
	}
}

func TestBunPublicationPassesOnlyScopedCredentialsAndVersion(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
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
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
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

func appendBunTemplate(registry *TemplateRegistry, template Template) *TemplateRegistry {
	if registry == nil {
		registry = &TemplateRegistry{WorkspaceFiles: []string{}}
	}
	registry.Templates = append(registry.Templates, template)
	return registry
}

func TestBunPublicationReusesCredentialsPerRegistry(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("bun-fixture")
	for _, test := range []struct{ name, registry string }{
		{"first", "https://registry.npmjs.org/"},
		{"second", "https://registry.npmjs.org/"},
		{"other", "https://npm.example.com/custom/"},
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
        grep -Fx '//npm.example.com/custom/:_authToken=shared-token' "$NPM_CONFIG_USERCONFIG" >/dev/null
        ! grep -F 'registry.npmjs.org' "$NPM_CONFIG_USERCONFIG" >/dev/null ;;
      *)
        grep -Fx '//registry.npmjs.org/:_authToken=shared-token' "$NPM_CONFIG_USERCONFIG" >/dev/null
        ! grep -F 'npm.example.com' "$NPM_CONFIG_USERCONFIG" >/dev/null ;;
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
	if len(paths) != 3 || paths[0] != paths[1] || paths[1] == paths[2] {
		t.Fatalf("credential files = %v; want one reused, one isolated", paths)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary credential file %q still exists: %v", path, err)
		}
	}
}
