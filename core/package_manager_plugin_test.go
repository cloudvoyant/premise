package core

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type testPackageManager struct {
	id        string
	filename  string
	builds    string
	buildErr  error
	publish   func(context.Context, string, string, string, io.Writer, io.Writer) error
	workspace func(context.Context, string, io.Writer, io.Writer) (string, bool, error)
}

func (p testPackageManager) ID() string { return p.id }
func (p testPackageManager) Detect(root string) (bool, error) {
	_, err := os.Stat(filepath.Join(root, p.filename))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
func (p testPackageManager) IsPublic(_ string, _ Template) (bool, error) { return false, nil }
func (p testPackageManager) ShouldPublishPackage(_ string, _ Template) (bool, error) {
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
	useTestPackageManagers(t, testPackageManager{id: "test", filename: "go.mod"})
	if err := RegisterPackageManagerPlugin(testPackageManager{id: "test"}); err == nil {
		t.Fatal("duplicate plugin ID accepted")
	}
	if err := RegisterPackageManagerPlugin(nil); err == nil {
		t.Fatal("nil plugin accepted")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plugin, err := packageManagerForRoot(root)
	if err != nil || plugin.ID() != "test" {
		t.Fatalf("selected package manager = %v, %v", plugin, err)
	}
}
