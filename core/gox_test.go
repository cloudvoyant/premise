package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Public API -----------------------------------------------------------------

func TestGoWorkspaceResolvesPrivateLibraryWithoutNetwork(t *testing.T) {
	root, _ := goWorkspaceFixture(t)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = canonical
	goWrite(t, root, "go.work", "go 1.25.0\nuse (\n ./apps/cli\n ./libs/private\n)\n")
	goWrite(t, root, "apps/cli/main.go", "package main\nimport \"example.com/repo/libs/private\"\nfunc main() { println(private.Value) }\n")
	command := exec.CommandContext(t.Context(), "go", "test", "./...")
	command.Dir = filepath.Join(root, "apps/cli")
	command.Env = append(withoutEnvironment(os.Environ(), "GOWORK", "GOPROXY"), "GOWORK="+filepath.Join(root, "go.work"), "GOPROXY=off")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("private local dependency: %v\n%s", err, data)
	}
}

func TestGoSharedVersionPublicConsumer(t *testing.T) {
	root, manifest := goWorkspaceFixture(t)
	tags, err := GoModuleTags(root, manifest, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(tags, ",") != "libs/math/v1.2.3,libs/usemath/v1.2.3" {
		t.Fatalf("tags = %v", tags)
	}
	if err := ValidateGoModuleRelease(t.Context(), root, manifest, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	modules, err := GoModules(root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	proxy := t.TempDir()
	if err := writeGoModuleProxy(proxy, modules, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	consumer := t.TempDir()
	goWrite(t, consumer, "go.mod", "module example.com/consumer\n\ngo 1.25.0\n\nrequire example.com/repo/libs/usemath v1.2.3\n")
	goWrite(t, consumer, "consumer_test.go", `package consumer
import (
 "testing"
 "example.com/repo/libs/usemath"
)
func TestExternal(t *testing.T) { if usemath.Value() != 42 { t.Fatal("bad result") } }
`)
	command := exec.CommandContext(t.Context(), "go", "test", "-mod=mod", "./...")
	command.Dir = consumer
	command.Env = goProxyEnvironment(proxy)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("external consumer: %v\n%s", err, data)
	}
	goWrite(t, root, "libs/usemath/go.mod", "module example.com/repo/libs/usemath\n\ngo 1.25.0\nrequire example.com/repo/libs/math v1.0.0\n")
	if _, err := GoModuleTags(root, manifest, "v1.2.3"); err == nil || !strings.Contains(err.Error(), "prepare") {
		t.Fatalf("old sibling dependency: %v", err)
	}
	if err := PrepareGoModuleDependencies(t.Context(), root, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGoModuleRelease(t.Context(), root, manifest, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "libs/usemath/go.sum"))
	if strings.Contains(string(data), "example.com/repo/libs/math v1.2.3") {
		t.Fatal("speculative local proxy checksum persisted")
	}
}

func TestGoReleaseRejectsInvalidPublication(t *testing.T) {
	for _, tc := range []struct{ name, content, version, want string }{
		{"private dependency", "module example.com/repo/libs/usemath\nrequire example.com/repo/libs/private v1.2.3\n", "v1.2.3", "unpublished"},
		{"local replace", "module example.com/repo/libs/usemath\nreplace example.com/external => ../math\n", "v1.2.3", "local replace"},
		{"major migration", "module example.com/repo/libs/usemath\n", "v2.0.0", "major-version"},
		{"wrong directory", "module example.com/repo/elsewhere\n", "v1.2.3", "repository directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, manifest := goWorkspaceFixture(t)
			if tc.version == "v2.0.0" {
				goWrite(t, root, "libs/math/go.mod", "module example.com/repo/libs/math/v2\n")
			}
			goWrite(t, root, "libs/usemath/go.mod", tc.content)
			if _, err := GoModuleTags(root, manifest, tc.version); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestGoGenerationCreatesNewWorkspace(t *testing.T) {
	root := t.TempDir()
	source := t.TempDir()
	goWrite(t, source, "go.mod", "module example.com/repo/apps/new\ngo 1.25.0\n")
	plan, err := BuildGeneratePlan(GenerateParameters{ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "apps/new", TemplateKind: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if err := plan.publishRoot(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "use ./apps/new") {
		t.Fatalf("new workspace: %s", data)
	}
}

func TestGoGenerationWorkspacePreservesModulesAndRollback(t *testing.T) {
	installMiseTestShim(t, false)
	root, manifest := goWorkspaceFixture(t)
	_ = manifest
	goWrite(t, root, "go.work", "// user comment\ngo 1.25.0\nuse ./libs/private\nreplace example.com/external v1.0.0 => example.com/external v1.0.1\n")
	source := t.TempDir()
	goWrite(t, source, "go.mod", "module example.com/repo/apps/new\ngo 1.25.0\n")
	goWrite(t, source, "main.go", "package main\nfunc main() {}\n")
	goWrite(t, source, "mise.toml", "[tasks.build]\nrun = 'echo build'\n")
	plan, err := BuildGeneratePlan(GenerateParameters{ClientRepoRoot: root, TemplateRoot: source, ProjectPath: "apps/new", TemplateKind: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if err := materializeGeneratePlan(plan, plan.stage); err != nil {
		t.Fatal(err)
	}
	candidate := t.TempDir()
	if err := prepareWorkspaceCandidate(plan, candidate, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(candidate, "libs/private/go.mod")); err != nil {
		t.Fatal("existing module not copied:", err)
	}
	data, _ := os.ReadFile(filepath.Join(candidate, "go.work"))
	for _, want := range []string{"user comment", "./libs/private", "./apps/new", "replace example.com/external"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s: %s", want, data)
		}
	}
	if strings.Contains(string(data), "premise-stage") {
		t.Fatal("staging path leaked")
	}
	original, _ := os.ReadFile(filepath.Join(root, "go.work"))
	if err := plan.publishRoot(); err != nil {
		t.Fatal(err)
	}
	if err := plan.rollbackRoot(); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(filepath.Join(root, "go.work"))
	if string(original) != string(restored) {
		t.Fatal("rollback lost original workspace")
	}
}

func TestGoCoordinatedTagPushAndConflict(t *testing.T) {
	root, manifest := goWorkspaceFixture(t)
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	remoteRoot := t.TempDir()
	remote, err := git.PlainInit(remoteRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteRoot}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	_, err = wt.Add(".")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := wt.Commit("feat: Go modules", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	auth := &http.BasicAuth{Username: "test", Password: "test"}
	for _, version := range []string{"v1.2.3", "v1.2.3"} {
		if err := createAndPushReleaseTag(t.Context(), root, version, auth); err != nil {
			t.Fatal(err)
		}
	}
	// Annotated tags at the same commit also converge without overwriting them.
	if err := remote.DeleteTag("v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.CreateTag("v1.2.3", hash, &git.CreateTagOptions{Tagger: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}, Message: "release"}); err != nil {
		t.Fatal(err)
	}
	if err := createAndPushReleaseTag(t.Context(), root, "v1.2.3", auth); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"v1.2.3", "libs/math/v1.2.3", "libs/usemath/v1.2.3"} {
		ref, err := remote.Tag(tag)
		if err != nil {
			t.Fatalf("remote tag %s: %v", tag, err)
		}
		target := ref.Hash()
		if obj, e := remote.TagObject(target); e == nil {
			target = obj.Target
		}
		if target != hash {
			t.Fatalf("remote tag %s at wrong commit", tag)
		}
	}
	if _, err := remote.Tag("libs/private/v1.2.3"); err == nil {
		t.Fatal("private module tagged")
	}
	// RC uses the same directory prefixes, with coordinated sibling RC requirements.
	goWrite(t, root, "libs/usemath/go.mod", "module example.com/repo/libs/usemath\ngo 1.25.0\nrequire example.com/repo/libs/math v1.3.0-rc.1\n")
	_, _ = wt.Add(".")
	rcHash, err := wt.Commit("feat: RC", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := createAndPushReleaseTag(t.Context(), root, "v1.3.0-rc.1", auth); err != nil {
		t.Fatal(err)
	}
	ref, err := remote.Tag("libs/math/v1.3.0-rc.1")
	if err != nil || ref.Hash() != rcHash {
		t.Fatalf("RC tag: %v", err)
	}
	// Pre-existing sibling conflict prevents even the root tag from being created.
	_, err = remote.CreateTag("libs/math/v1.3.0", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	goWrite(t, root, "libs/usemath/go.mod", "module example.com/repo/libs/usemath\ngo 1.25.0\nrequire example.com/repo/libs/math v1.3.0\n")
	_, _ = wt.Add(".")
	_, err = wt.Commit("feat: stable", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	_ = manifest
	if err := createAndPushReleaseTag(t.Context(), root, "v1.3.0", auth); err == nil || !strings.Contains(err.Error(), "away from HEAD") {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := repo.Tag("v1.3.0"); err == nil {
		t.Fatal("root tag created before conflict checked")
	}
}

// Private helpers ------------------------------------------------------------

func goWorkspaceFixture(t *testing.T) (string, Config) {
	t.Helper()
	root := t.TempDir()
	manifest := NewManifest("mixed")
	manifest.Workspace.Kind = ProjectKindMonorepo
	manifest.Workspace.PackageManagers = []string{"go", "bun", "cargo"}
	for _, path := range []string{"apps/cli", "apps/service", "libs/math", "libs/private", "libs/usemath"} {
		name := filepath.Base(path)
		publication := "private-local"
		main := ""
		if strings.HasPrefix(path, "apps/") {
			main = "."
		}
		if name == "math" || name == "usemath" {
			publication = "public"
		}
		manifest.Workspace.Projects = append(manifest.Workspace.Projects, Project{Name: name, Path: path, Template: "test:" + name})
		goWrite(t, root, path+"/go.mod", "module example.com/repo/"+path+"\n\ngo 1.25.0\n")
		goWrite(t, root, path+"/.premise-go.toml", "publication = \""+publication+"\"\nmain = \""+main+"\"\n")
		source := "package " + name + "\nconst Value = 42\n"
		if main != "" {
			source = "package main\nfunc main() {}\n"
		}
		goWrite(t, root, path+"/main.go", source)
	}
	goWrite(t, root, "libs/math/main.go", "package math\nconst Value = 42\n")
	goWrite(t, root, "libs/usemath/go.mod", "module example.com/repo/libs/usemath\n\ngo 1.25.0\nrequire example.com/repo/libs/math v1.2.3\n")
	goWrite(t, root, "libs/usemath/main.go", "package usemath\nimport \"example.com/repo/libs/math\"\nfunc Value() int { return math.Value }\n")
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	return root, manifest
}

func goWrite(t *testing.T, root, path, content string) {
	t.Helper()
	destination := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
