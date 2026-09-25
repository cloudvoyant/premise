package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	plugin, err := releasePluginForRoot(root)
	if err != nil || plugin.Profile() != ReleaseProfileBun {
		t.Fatalf("detect Bun plugin = %v, %v", plugin, err)
	}
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
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	config, err := plugin.CreateGoReleaserConfig(root, manifest)
	if err != nil || config != "" {
		t.Fatalf("Bun downloadable config = %q, %v", config, err)
	}
	var output bytes.Buffer
	if err := runGoReleaser(t.Context(), root, ReleaseProfileBun, false, &output, &output); err != nil {
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
