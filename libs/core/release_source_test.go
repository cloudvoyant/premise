package core

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"go.yaml.in/yaml/v3"
)

// Tests ----------------------------------------------------------------------

func TestSourceOnlyGoReleaserConfig(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("lua-plugin")
	manifest.Workspace.PackageManagers = []string{}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	config, err := GoReleaserConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Builds   []struct{ Skip bool }       `yaml:"builds"`
		Checksum struct{ Disable bool }      `yaml:"checksum"`
		Release  struct{ Prerelease string } `yaml:"release"`
	}
	if err := yaml.Unmarshal(config, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Builds) != 1 || !parsed.Builds[0].Skip || !parsed.Checksum.Disable {
		t.Fatalf("source-only configuration enables artifacts: %s", config)
	}
	if parsed.Release.Prerelease != "auto" || !strings.Contains(string(config), "make_latest:") {
		t.Fatalf("source-only configuration lost release policy: %s", config)
	}
	var output bytes.Buffer
	// A snapshot of a source-only library needs neither publication credentials nor tools.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if err := BuildReleaseArtifacts(t.Context(), root, &output, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "skip: workspace has no downloadable release artifacts") {
		t.Fatal(output.String())
	}
}

func TestPublishReleaseRejectsBackendConfigurationBeforeTagging(t *testing.T) {
	for _, tc := range []struct{ name, manager, fragment, expected string }{
		{"unknown", "unknown", "", "not registered"},
		{"invalid-fragment", "invalid", "builds: [", "parse invalid artifact configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTaggedCargoReleaseFixture(t)
			repository, err := git.PlainOpen(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.DeleteTag("v1.2.3"); err != nil {
				t.Fatal(err)
			}
			manifest := NewManifest("lua-plugin")
			manifest.Workspace.PackageManagers = []string{tc.manager}
			if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
				t.Fatal(err)
			}
			useTestPackageManagers(t, testPackageManager{id: "invalid", builds: tc.fragment})
			t.Setenv("GITHUB_TOKEN", "test-token")
			originalPush := pushReleaseTag
			t.Cleanup(func() { pushReleaseTag = originalPush })
			pushReleaseTag = func(context.Context, string, string, transport.AuthMethod) error {
				t.Fatal("invalid configuration reached tag creation")
				return nil
			}
			for _, channel := range []string{"stable", "rc"} {
				version := "v0.1.0"
				if channel == "rc" {
					version += "-rc.42"
				}
				_, err = PublishRelease(t.Context(), root, ReleasePublishOptions{Channel: channel, ExpectedVersion: version}, io.Discard, io.Discard)
				if err == nil || !strings.Contains(err.Error(), tc.expected) {
					t.Fatalf("%s publication error = %v, want %q", channel, err, tc.expected)
				}
			}
		})
	}
}

func TestPublishLanguagePackagesSkipsSourceOnlyRelease(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	manifest := NewManifest("lua-plugin")
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	plan, err := PublishLanguagePackages(t.Context(), root, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != "v1.2.3" || !strings.Contains(output.String(), "skip: workspace package managers do not publish language packages") {
		t.Fatalf("source-only package publication = %#v, %s", plan, output.String())
	}
}
