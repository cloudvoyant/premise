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

func TestGoProvider(t *testing.T) {
	t.Run("when reading package metadata", func(t *testing.T) {
		t.Run("when a library is public", func(t *testing.T) {
			root := goPackageFixture(t)
			target := core.Template{Name: "math", Path: "libs/math", Kind: "lib", Package: &core.PackageDeclaration{Public: boolPointer(true)}}

			metadata, found, err := (Go{}).GetPackageMetadata(root, target)

			t.Run("it identifies public packages under the root module path", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				if !found || !metadata.Publishable || metadata.Name != "example.com/workspace/v4/libs/math" {
					t.Fatalf("expected public package at the root module path, got %+v (found=%v)", metadata, found)
				}
			})
		})
		t.Run("when library visibility is omitted", func(t *testing.T) {
			root := goPackageFixture(t)
			target := core.Template{Name: "math", Path: "libs/math", Kind: "lib"}

			metadata, found, err := (Go{}).GetPackageMetadata(root, target)

			t.Run("it defaults to public when the setting is omitted", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				if !found || !metadata.Public || !metadata.Publishable {
					t.Fatalf("expected a public package by default, got %+v", metadata)
				}
			})
		})

		t.Run("when a library is private", func(t *testing.T) {
			root := goPackageFixture(t)
			target := core.Template{Name: "math", Path: "libs/math", Kind: "lib", Package: &core.PackageDeclaration{Public: boolPointer(false)}}

			ready, err := (Go{}).WillPublishOk(t.Context(), root, target, "v4.0.0-rc.42", "publish:rc")

			t.Run("it skips publication when public is false", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				if ready {
					t.Fatal("public false did not skip the publication task")
				}
			})
		})
	})

	t.Run("when validating a shared module release", func(t *testing.T) {
		t.Run("when a public library uses a committed local helper", func(t *testing.T) {
			root := goPackageFixture(t)
			t.Setenv("MISE_TRUSTED_CONFIG_PATHS", root)

			err := (Go{}).PreflightPublication(t.Context(), root, "v4.0.0-rc.42", "publish:rc")

			t.Run("it accepts a public library that uses a local helper", func(t *testing.T) {
				if err != nil {
					t.Fatalf("expected release validation to succeed: %v", err)
				}
			})
		})

		t.Run("when a library depends on ignored source files", func(t *testing.T) {
			root := goPackageFixture(t)
			t.Setenv("MISE_TRUSTED_CONFIG_PATHS", root)
			writePackageFixtureFile(t, root, ".gitignore", "libs/math/generated.go\n")
			writePackageFixtureFile(t, root, "libs/math/math.go", "package math\nfunc Value() int { return generatedValue }\n")
			writePackageFixtureFile(t, root, "libs/math/generated.go", "package math\nconst generatedValue = 42\n")
			goFixtureGit(t, root, "add", ".gitignore", "libs/math/math.go")
			goFixtureGit(t, root, "commit", "-m", "feat: depend on generated source")

			err := (Go{}).PreflightPublication(t.Context(), root, "v4.0.0-rc.43", "publish:rc")

			t.Run("it rejects dependencies on ignored source files", func(t *testing.T) {
				if err == nil || !strings.Contains(err.Error(), "committed Go package") {
					t.Fatalf("expected committed-source validation to fail, got %v", err)
				}
			})
		})
		t.Run("when source changes are uncommitted", func(t *testing.T) {
			root := goPackageFixture(t)
			t.Setenv("MISE_TRUSTED_CONFIG_PATHS", root)
			writePackageFixtureFile(t, root, "libs/math/math.go", "package math\nconst Changed = 42\n")

			err := (Go{}).PreflightPublication(t.Context(), root, "v4.0.0-rc.42", "publish:rc")

			t.Run("it rejects uncommitted source changes", func(t *testing.T) {
				if err == nil || !strings.Contains(err.Error(), "requires a clean committed checkout") {
					t.Fatalf("uncommitted source passed publication checks: %v", err)
				}
			})
		})

		t.Run("when release validation finishes", func(t *testing.T) {
			root := goPackageFixture(t)
			t.Setenv("MISE_TRUSTED_CONFIG_PATHS", root)
			snapshots := t.TempDir()
			t.Setenv("TMPDIR", snapshots)

			err := (Go{}).PreflightPublication(t.Context(), root, "v4.0.0-rc.42", "publish:rc")

			t.Run("it removes temporary validation files", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				remaining, err := filepath.Glob(filepath.Join(snapshots, "premise-package-snapshot-*"))
				if err != nil || len(remaining) != 0 {
					t.Fatalf("validation files remain: %v (%v)", remaining, err)
				}
			})
		})
	})
	t.Run("when generating the first Go project", func(t *testing.T) {
		root, source := t.TempDir(), t.TempDir()
		writePackageFixtureFile(t, source, "go.mod", "module example.com/repository/libs/math\ngo 1.25.0\n")

		entries, err := (Go{}).PrepareGeneration(generationTestContext(t, root, source, "libs/math", (Go{}).GenerationRootFiles()))

		t.Run("it uses the repository module path in the root manifest", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Path != "go.mod" || !strings.Contains(string(entries[0].Output.Data), "module example.com/repository\n") {
				t.Fatalf("root module uses the project path: %v", entries)
			}
		})
	})

	t.Run("when generating a library in an existing workspace", func(t *testing.T) {
		root, source := goGenerationFixture(t)

		entries, err := (Go{}).PrepareGeneration(generationTestContext(t, root, source, "libs/math", (Go{}).GenerationRootFiles()))
		if err != nil {
			t.Fatal(err)
		}

		t.Run("it combines dependencies in the root manifest", func(t *testing.T) {
			if len(entries) != 1 || !strings.Contains(string(entries[0].Output.Data), "example.com/dependency v1.2.0") {
				t.Fatalf("expected the newer dependency in the root manifest, got %v", entries)
			}
		})
		t.Run("it preserves the workspace module path", func(t *testing.T) {
			if len(entries) != 1 || !strings.Contains(string(entries[0].Output.Data), "module example.com/shared\n") {
				t.Fatalf("expected the shared module path, got %v", entries)
			}
		})
		t.Run("it removes the staged project manifest", func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(source, "go.mod")); !os.IsNotExist(err) {
				t.Fatalf("staged project retains a module manifest: %v", err)
			}
		})
		t.Run("it leaves the existing workspace unchanged during planning", func(t *testing.T) {
			original, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(original), "v1.0.0") {
				t.Fatalf("planning changed the workspace manifest: %s", original)
			}
		})
	})

	t.Run("when building release artifacts", func(t *testing.T) {
		t.Run("when the workspace contains applications", func(t *testing.T) {
			root := goArtifactFixture(t, true)

			config, err := core.GoReleaserConfig(root)

			t.Run("it creates one archive for each application", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"dir: apps/cli", "dir: apps/service", "id: go-cli", "id: go-service", "binary: service-custom"} {
					if !strings.Contains(string(config), want) {
						t.Errorf("application release omits %q", want)
					}
				}
				if strings.Contains(string(config), "id: go-math") || strings.Contains(string(config), "binary: shared") {
					t.Fatal("library or root invents a binary")
				}
			})
		})

		t.Run("when the workspace contains only libraries", func(t *testing.T) {
			root := goArtifactFixture(t, false)

			config, err := core.GoReleaserConfig(root)

			t.Run("it creates a source-only release for libraries", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(config), "skip: true") || strings.Contains(string(config), "binary:") {
					t.Fatalf("library produces executable artifacts: %s", config)
				}
			})
		})
	})
}

func TestGoGeneration(t *testing.T) {
	registerBuiltins(t)
	t.Run("when existing Go configuration defines multiple modules", func(t *testing.T) {
		for _, path := range []string{"go.work", "libs/existing/go.mod"} {
			t.Run("when existing configuration contains "+path, func(t *testing.T) {
				root, stage := t.TempDir(), t.TempDir()
				original := "module example.com/standalone\ngo 1.25.0\n"
				if path == "go.work" {
					original = "go 1.25.0\nuse ./libs/existing\n"
				}
				writePackageFixtureFile(t, root, path, original)
				writePackageFixtureFile(t, stage, "go.mod", "module example.com/workspace/libs/new\ngo 1.25.0\n")

				plan, err := core.BuildGeneratePlan(core.GenerateParameters{PackageManagers: []string{"go"}, ProjectPaths: []string{"libs/existing"}, ClientRepoRoot: root, TemplateRoot: stage, ProjectPath: "libs/new", TemplateKind: "lib"})

				t.Run("it rejects the layout without converting client files", func(t *testing.T) {
					if plan != nil {
						discardGenerationPlan(t, plan, root, "libs/new")
					}
					if err == nil || !strings.Contains(err.Error(), "root module") {
						t.Fatalf("expected existing module layout error: %v", err)
					}
					data, readErr := os.ReadFile(filepath.Join(root, path))
					if readErr != nil || string(data) != original {
						t.Fatal("existing Go layout was changed")
					}
					if _, err := os.Stat(filepath.Join(root, "libs/new")); !os.IsNotExist(err) {
						t.Fatalf("new project exists after layout rejection: %v", err)
					}
				})
			})
		}
	})
}

// Private helpers ------------------------------------------------------------

func goPackageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("..", "testdata", "go-workspace")
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
	manifest.Workspace.Projects = []core.Project{{Name: "math", Path: "libs/math", Template: "test:math", Package: &core.PackageDeclaration{Public: boolPointer(true)}}, {Name: "cache", Path: "libs/cache", Template: "test:cache", Package: &core.PackageDeclaration{Public: boolPointer(false)}}}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	goFixtureGit(t, root, "init", "-b", "main")
	goFixtureGit(t, root, "add", ".")
	goFixtureGit(t, root, "commit", "-m", "feat: shared packages")
	return root
}

func goFixtureGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
	command.Dir = root
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, data)
	}
}

func goGenerationFixture(t *testing.T) (string, string) {
	t.Helper()
	root, source := t.TempDir(), t.TempDir()
	writePackageFixtureFile(t, root, "go.mod", "module example.com/shared\ngo 1.25.0\nrequire example.com/dependency v1.0.0\n")
	writePackageFixtureFile(t, source, "go.mod", "module example.com/shared/libs/math\ngo 1.25.0\nrequire example.com/dependency v1.2.0\n")
	return root, source
}

func goArtifactFixture(t *testing.T, applications bool) string {
	t.Helper()
	registerBuiltins(t)
	root := goPackageFixture(t)
	if !applications {
		return root
	}
	manifest, err := core.LoadManifest(filepath.Join(root, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cli", "service"} {
		relative := "apps/" + name
		declaration := &core.PackageDeclaration{Main: "."}
		if name == "service" {
			declaration.Binary = "service-custom"
		}
		manifest.Workspace.Projects = append(manifest.Workspace.Projects, core.Project{Name: name, Path: relative, Template: "test:" + name, Package: declaration})
		writePackageFixtureFile(t, root, relative+"/main.go", "package main\nfunc main() {}\n")
	}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	return root
}

func boolPointer(value bool) *bool { return &value }
