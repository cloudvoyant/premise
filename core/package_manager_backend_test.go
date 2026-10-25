package core

import (
	"context"
	"errors"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"io"
	"path/filepath"
	"testing"
)

// Fixtures -------------------------------------------------------------------

type testPackageManager struct {
	repositoryPackages bool
	id                 string
	ecosystem          string
	builds             string
	buildErr           error
	publish            func(context.Context, string, string, string, io.Writer, io.Writer) error
	workspace          func(context.Context, string, io.Writer, io.Writer) (string, bool, error)
	preflight          func(context.Context, string, string, string) error
}

// Tests ----------------------------------------------------------------------

func TestPackageManagerRegistration(t *testing.T) {
	useTestPackageManagers(t, testPackageManager{id: "test"})
	if err := RegisterPackageManagerBackend(testPackageManager{id: "test"}); err == nil {
		t.Fatal("duplicate plugin ID accepted")
	}
	if err := RegisterPackageManagerBackend(nil); err == nil {
		t.Fatal("nil plugin accepted")
	}
	manifest := NewManifest("test")
	manifest.Workspace.PackageManagers = []string{"test"}
	plugins, err := packageManagersForConfig(manifest)
	if err != nil || len(plugins) != 1 || plugins[0].ID() != "test" {
		t.Fatalf("selected package managers = %v, %v", plugins, err)
	}
}

func TestPackageManagerSelectionRejectsConflictingEcosystems(t *testing.T) {
	useTestPackageManagers(t,
		testPackageManager{id: "bun", ecosystem: "npm"},
		testPackageManager{id: "pnpm", ecosystem: "npm"},
	)
	manifest := NewManifest("test")
	manifest.Workspace.PackageManagers = []string{"bun", "pnpm"}
	if _, err := packageManagersForConfig(manifest); err == nil {
		t.Fatal("conflicting npm package managers accepted")
	}
}

func TestPackageManagerSelectionWithoutBackends(t *testing.T) {
	useTestPackageManagers(t, testPackageManager{id: "unused", buildErr: io.ErrUnexpectedEOF})
	for _, managers := range [][]string{nil, {}} {
		manifest := NewManifest("lua-plugin")
		manifest.Workspace.PackageManagers = managers
		plugins, err := packageManagersForConfig(manifest)
		if err != nil || len(plugins) != 0 {
			t.Fatalf("empty selection = %v, %v", plugins, err)
		}
		root := t.TempDir()
		workspace, serial, err := prepareReleaseWorkspace(t.Context(), root, manifest, plugins, io.Discard, io.Discard)
		if err != nil || workspace != root || serial {
			t.Fatalf("empty release workspace = %q, %t, %v", workspace, serial, err)
		}
	}
}

func TestPackageManagerSelectionRejectsUnknownID(t *testing.T) {
	useTestPackageManagers(t)
	manifest := NewManifest("lua-plugin")
	manifest.Workspace.PackageManagers = []string{"generic"}
	if _, err := packageManagersForConfig(manifest); err == nil {
		t.Fatal("unknown manager accepted")
	}
}

func TestPackageManagerPublication(t *testing.T) {
	t.Run("when repository package validation fails", func(t *testing.T) {
		root := writeTaggedCargoReleaseFixture(t)
		manifest := NewManifest("future-packages")
		manifest.Workspace.PackageManagers = []string{"future"}
		if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
			t.Fatal(err)
		}
		expected := errors.New("package preflight failed")
		preflightCalled, pushed := false, false
		provider := testPackageManager{
			repositoryPackages: true,
			id:                 "future",
			preflight: func(_ context.Context, _ string, version, task string) error {
				preflightCalled = true
				if version != "v1.3.0-rc.42" || task != "publish:rc" {
					t.Fatalf("wrong provider inputs %s %s", version, task)
				}
				return expected
			},
		}
		useTestPackageManagers(t, provider)
		oldPush := pushReleaseTag
		pushReleaseTag = func(context.Context, string, string, transport.AuthMethod) error { pushed = true; return nil }
		t.Cleanup(func() { pushReleaseTag = oldPush })
		t.Setenv("GITHUB_TOKEN", "test-token")

		_, err := PublishRelease(t.Context(), root, ReleasePublishOptions{Channel: "rc", ExpectedVersion: "v1.3.0-rc.42"}, io.Discard, io.Discard)

		t.Run("it stops publication before creating a tag", func(t *testing.T) {
			if !errors.Is(err, expected) || !preflightCalled || pushed {
				t.Fatalf("publication bypassed generic preflight: called=%v pushed=%v err=%v", preflightCalled, pushed, err)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func useTestPackageManagers(t *testing.T, plugins ...PackageManagerBackend) {
	t.Helper()
	packageManagerRegistry.Lock()
	original := packageManagerRegistry.plugins
	packageManagerRegistry.plugins = nil
	packageManagerRegistry.Unlock()
	t.Cleanup(func() {
		packageManagerRegistry.Lock()
		packageManagerRegistry.plugins = original
		packageManagerRegistry.Unlock()
	})
	for _, plugin := range plugins {
		if err := RegisterPackageManagerBackend(plugin); err != nil {
			t.Fatal(err)
		}
	}
}

func (p testPackageManager) GenerationRootFiles() []string { return nil }
func (p testPackageManager) PrepareGeneration(PackageGenerationContext) ([]MergeEntry, error) {
	return nil, nil
}
func (p testPackageManager) ReleaseTools(string, Config) ([]string, error) { return nil, nil }

func (p testPackageManager) ParseDependencies([]byte) ([]Dependency, error) { return nil, nil }
func (p testPackageManager) WriteDependencies(manifest []byte, _ []Dependency) ([]byte, error) {
	return append([]byte(nil), manifest...), nil
}

func (p testPackageManager) ID() string { return p.id }
func (p testPackageManager) Ecosystem() string {
	if p.ecosystem != "" {
		return p.ecosystem
	}
	return p.id
}
func (p testPackageManager) GetPackageMetadata(_ string, _ Template) (PackageMetadata, bool, error) {
	return PackageMetadata{}, false, nil
}
func (p testPackageManager) ValidatePackage(_ string, _ Template) error { return nil }
func (p testPackageManager) WillPublishOk(_ context.Context, _ string, _ Template, _, _ string) (bool, error) {
	return false, nil
}
func (p testPackageManager) PreflightPublication(ctx context.Context, root, version, task string) error {
	if p.preflight != nil {
		return p.preflight(ctx, root, version, task)
	}
	return nil
}
func (p testPackageManager) SupportsPackages() bool { return p.publish != nil }
func (p testPackageManager) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	if p.publish == nil {
		return nil
	}
	return p.publish(ctx, root, version, task, stdout, stderr)
}
func (p testPackageManager) ReleaseWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
	if p.workspace != nil {
		return p.workspace(ctx, root, stdout, stderr)
	}
	return root, false, nil
}
func (p testPackageManager) CreateGoReleaserConfig(_ string, _ Config) (string, error) {
	return p.builds, p.buildErr
}

func (p testPackageManager) PublishesFromGitTag() bool { return p.repositoryPackages }
