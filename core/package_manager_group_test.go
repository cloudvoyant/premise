package core

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixedPackageManagers(t *testing.T) {
	root := t.TempDir()
	if err := SaveManifest(filepath.Join(root, ManifestFilename), NewManifest("mixed")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "Cargo.toml", "bunfig.toml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var published []string
	var prepared []string
	publish := func(id string) func(context.Context, string, string, string, io.Writer, io.Writer) error {
		return func(_ context.Context, _, version, task string, _, _ io.Writer) error {
			published = append(published, id+":"+version+":"+task)
			return nil
		}
	}
	workspace := func(id string, serial bool) func(context.Context, string, io.Writer, io.Writer) (string, bool, error) {
		return func(_ context.Context, root string, _, _ io.Writer) (string, bool, error) {
			prepared = append(prepared, id)
			return root, serial, nil
		}
	}
	useTestPackageManagers(t,
		testPackageManager{id: "go", filename: "go.mod", builds: "builds:\n  - id: go-app\narchives:\n  - id: go-archive\n", workspace: workspace("go", false)},
		testPackageManager{id: "cargo", filename: "Cargo.toml", builds: "builds:\n  - id: rust-app\narchives:\n  - id: rust-archive\n", workspace: workspace("cargo", true), publish: publish("cargo")},
		testPackageManager{id: "bun", filename: "bunfig.toml", publish: publish("bun"), workspace: workspace("bun", false)},
	)
	plugin, err := packageManagerForRoot(root)
	if err != nil || plugin.ID() != "go,cargo,bun" {
		t.Fatalf("packageManagerForRoot = %v, %v", plugin, err)
	}
	configuration, err := GoReleaserConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id: go-app", "id: rust-app", "id: go-archive", "id: rust-archive"} {
		if !strings.Contains(string(configuration), want) {
			t.Errorf("combined configuration missing %q: %s", want, configuration)
		}
	}
	var output bytes.Buffer
	if err := plugin.PublishPackages(t.Context(), root, "v1.2.3", "publish", &output, &output); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(published, ","); got != "cargo:v1.2.3:publish,bun:v1.2.3:publish" {
		t.Fatalf("published = %q", got)
	}
	if directory, serial, err := plugin.ReleaseWorkspace(t.Context(), root, &output, &output); err != nil || directory != root || !serial {
		t.Fatalf("ReleaseWorkspace = %q, %v, %v", directory, serial, err)
	}
	if got := strings.Join(prepared, ","); got != "go,cargo" {
		t.Fatalf("prepared = %q; Bun has no artifacts and should be skipped", got)
	}
}

func TestMixedPackageManagersRejectDuplicateArtifacts(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"go.mod", "Cargo.toml"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	useTestPackageManagers(t,
		testPackageManager{id: "go", filename: "go.mod", builds: "builds:\n  - id: shared\n"},
		testPackageManager{id: "cargo", filename: "Cargo.toml", builds: "builds:\n  - id: shared\n"},
	)
	plugin, err := packageManagerForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.CreateGoReleaserConfig(root, NewManifest("mixed")); err == nil || !strings.Contains(err.Error(), "duplicate builds artifact ID") {
		t.Fatalf("duplicate IDs accepted: %v", err)
	}
}
