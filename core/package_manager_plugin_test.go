package core

import (
	"context"
	"io"
	"testing"
)

type testPackageManager struct {
	id        string
	ecosystem string
	builds    string
	buildErr  error
	publish   func(context.Context, string, string, string, io.Writer, io.Writer) error
	workspace func(context.Context, string, io.Writer, io.Writer) (string, bool, error)
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

func useTestPackageManagers(t *testing.T, plugins ...PackageManagerPlugin) {
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
		if err := RegisterPackageManagerPlugin(plugin); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPackageManagerRegistration(t *testing.T) {
	useTestPackageManagers(t, testPackageManager{id: "test"})
	if err := RegisterPackageManagerPlugin(testPackageManager{id: "test"}); err == nil {
		t.Fatal("duplicate plugin ID accepted")
	}
	if err := RegisterPackageManagerPlugin(nil); err == nil {
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
