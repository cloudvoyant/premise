package backends

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
)

// Tests ----------------------------------------------------------------------

func TestGoProviderPublishesCoupledPackagesAtTheRootVersion(t *testing.T) {
	root := goPackageFixture(t)
	t.Setenv("MISE_TRUSTED_CONFIG_PATHS", root)
	snapshots := t.TempDir()
	t.Setenv("TMPDIR", snapshots)
	provider := Go{}
	metadata, found, err := provider.GetPackageMetadata(root, core.Template{Name: "math", Path: "libs/math", Kind: "lib"})
	if err != nil || !found || !metadata.Publishable || metadata.Name != "example.com/workspace/v4/libs/math" {
		t.Fatalf("metadata %v %v %v", metadata, found, err)
	}

	err = provider.PreflightPublication(t.Context(), root, "v4.0.0-rc.42", "publish:rc")

	if err != nil {
		t.Fatal(err)
	}
	remaining, err := filepath.Glob(filepath.Join(snapshots, "premise-package-snapshot-*"))
	if err != nil || len(remaining) != 0 {
		t.Fatalf("publication snapshot was not cleaned up: %v %v", remaining, err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); !os.IsNotExist(err) {
		t.Fatal("shared module unexpectedly needs go.work")
	}
	if _, err := os.Stat(filepath.Join(root, "libs/math/go.mod")); !os.IsNotExist(err) {
		t.Fatal("library has a nested module")
	}
}

func TestGoProviderRejectsIgnoredSourceMissingFromTheRelease(t *testing.T) {
	root := goPackageFixture(t)
	t.Setenv("MISE_TRUSTED_CONFIG_PATHS", root)
	goFixtureWrite(t, root, ".gitignore", "libs/math/generated.go\n")
	goFixtureWrite(t, root, "libs/math/math.go", "package math\nfunc Value() int { return generatedValue }\n")
	goFixtureWrite(t, root, "libs/math/generated.go", "package math\nconst generatedValue = 42\n")
	goFixtureGit(t, root, "add", ".gitignore", "libs/math/math.go")
	goFixtureGit(t, root, "commit", "-m", "feat: depend on generated source")

	err := (Go{}).PreflightPublication(t.Context(), root, "v4.0.0-rc.43", "publish:rc")

	if err == nil || !strings.Contains(err.Error(), "committed Go package") {
		t.Fatalf("ignored source passed publication checks: %v", err)
	}
}

func TestGoProviderGenerationCombinesDependenciesAndKeepsOneModule(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	goFixtureWrite(t, root, "go.mod", "module example.com/shared\ngo 1.25.0\nrequire example.com/dependency v1.0.0\n")
	goFixtureWrite(t, source, "go.mod", "module example.com/shared/libs/math\ngo 1.25.0\nrequire example.com/dependency v1.2.0\n")

	entries, err := (Go{}).PrepareGeneration(core.PackageGenerationContext{WorkspaceRoot: root, ProjectPath: "libs/math", Stage: source})

	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(string(entries[0].Output.Data), "example.com/dependency v1.2.0") {
		t.Fatalf("dependency merge: %v", entries)
	}
	if strings.Contains(string(entries[0].Output.Data), "module example.com/shared/libs/math") {
		t.Fatal("project path became workspace module")
	}
	if _, err := os.Stat(filepath.Join(source, "go.mod")); !os.IsNotExist(err) {
		t.Fatal("staged package retained a nested go.mod")
	}
	original, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(original), "v1.0.0") {
		t.Fatal("planning changed source files")
	}
}

// Private helpers ------------------------------------------------------------

func goPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("testdata", "go-workspace")
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(root, relative), 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, relative), data, 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := core.NewManifest("shared")
	manifest.Workspace.Kind = core.ProjectKindMonorepo
	manifest.Workspace.PackageManagers = []string{"go"}
	manifest.Workspace.Projects = []core.Project{{Name: "math", Path: "libs/math", Template: "test:math"}, {Name: "cache", Path: "libs/cache", Template: "test:cache"}}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	goFixtureGit(t, root, "init", "-b", "main")
	goFixtureGit(t, root, "add", ".")
	goFixtureGit(t, root, "commit", "-m", "feat: shared packages")
	return root
}

func goFixtureWrite(t *testing.T, root, path, text string) {
	t.Helper()
	destination := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func goFixtureGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
	command.Dir = root
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, data)
	}
}
