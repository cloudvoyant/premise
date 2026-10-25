package backends

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
	"golang.org/x/mod/modfile"
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
		t.Run("when a public library uses a local helper", func(t *testing.T) {
			root := goPackageFixture(t)
			t.Setenv("MISE_TRUSTED_CONFIG_PATHS", root)

			err := (Go{}).PreflightPublication(t.Context(), root, "v4.0.0-rc.42", "publish:rc")

			t.Run("it accepts a public library that uses a local helper", func(t *testing.T) {
				if err != nil {
					t.Fatalf("expected release validation to succeed: %v", err)
				}
			})
		})

	})
	t.Run("when generating the first Go project", func(t *testing.T) {
		root, source := t.TempDir(), t.TempDir()
		writePackageFixtureFile(t, source, "go.mod", "module example.com/repository/libs/math\ngo 1.25.0\n")

		entries, err := (Go{}).PrepareGeneration(generationTestContext(t, root, source, "libs/math", (Go{}).ManagedWorkspaceFiles()))

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

		entries, err := (Go{}).PrepareGeneration(generationTestContext(t, root, source, "libs/math", (Go{}).ManagedWorkspaceFiles()))
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
	t.Run("when the project has no Go module", func(t *testing.T) {
		// Arrange
		root, stage, registry := t.TempDir(), t.TempDir(), t.TempDir()
		original := "module example.com/client\ngo 1.25.0\n"
		writePackageFixtureFile(t, root, "go.mod", original)
		writePackageFixtureFile(t, stage, "README.md", "generic project")
		writePackageFixtureFile(t, registry, "go.mod", "module example.com/registry\ngo 1.25.0\n")

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{
			PackageManagers:        []string{"go"},
			RegistryRoot:           registry,
			RegistryWorkspaceFiles: []string{"go.mod"},
			ClientRepoRoot:         root,
			TemplateRoot:           stage,
			ProjectPath:            "libs/generic",
			TemplateKind:           "lib",
			ResolveConflict:        core.MergeDecisions{}.Resolve,
		})
		if err != nil {
			t.Fatal(err)
		}
		discardGenerationPlan(t, plan, root, "libs/generic")

		// Assert
		t.Run("it leaves the existing module untouched without a native contribution", func(t *testing.T) {
			for _, entry := range plan.Entries {
				if entry.Path == "go.mod" || entry.Path == "go.sum" {
					t.Fatalf("unexpected native output: %#v", entry)
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil || string(data) != original {
				t.Fatalf("module changed: %s %v", data, err)
			}
		})
	})
	t.Run("when existing Go configuration defines multiple modules", func(t *testing.T) {
		for _, path := range []string{"go.work", "libs/existing/go.mod"} {
			t.Run("when existing configuration contains "+path, func(t *testing.T) {
				// Arrange
				root, stage := t.TempDir(), t.TempDir()
				original := "module example.com/standalone\ngo 1.25.0\n"
				if path == "go.work" {
					original = "go 1.25.0\nuse ./libs/existing\n"
				}
				writePackageFixtureFile(t, root, path, original)
				writePackageFixtureFile(t, stage, "go.mod", "module example.com/workspace/libs/new\ngo 1.25.0\n")

				// Act
				plan, err := core.BuildGeneratePlan(core.GenerateParameters{
					PackageManagers: []string{"go"},
					ProjectPaths:    []string{"libs/existing"},
					ClientRepoRoot:  root,
					TemplateRoot:    stage,
					ProjectPath:     "libs/new",
					TemplateKind:    "lib",
				})

				// Assert
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
	t.Run("when generating a Bun project beside an unrelated Go workspace", func(t *testing.T) {
		// Arrange
		root, source := t.TempDir(), t.TempDir()
		original := "go 1.25.0\nuse /outside/workspace\n"
		writePackageFixtureFile(t, root, "go.work", original)
		writePackageFixtureFile(t, source, "package.json", `{"name":"web"}`)

		// Act
		plan, err := core.BuildGeneratePlan(core.GenerateParameters{
			PackageManagers: []string{"bun"},
			ClientRepoRoot:  root,
			TemplateRoot:    source,
			ProjectPath:     "apps/web",
			TemplateKind:    "app",
		})
		if err != nil {
			t.Fatal(err)
		}
		discardGenerationPlan(t, plan, root, "apps/web")

		// Assert
		t.Run("it prepares Bun files without applying Go workspace rules", func(t *testing.T) {
			if !strings.Contains(string(generationOutput(t, plan, "package.json")), "workspaces") {
				t.Fatal("Bun workspace preparation was skipped")
			}
			data, err := os.ReadFile(filepath.Join(root, "go.work"))
			if err != nil || string(data) != original {
				t.Fatalf("unselected Go workspace changed: %q %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(root, "go.mod")); !os.IsNotExist(err) {
				t.Fatalf("unselected Go module was created: %v", err)
			}
		})
	})

}

func TestGoDependencyManifests(t *testing.T) {
	t.Run("when parsing and regenerating a Go module", func(t *testing.T) {
		// Arrange
		manifest := readPackageManifestFixture(t, "go.mod")

		// Act
		dependencies, err := (Go{}).ParseDependencies(manifest)
		if err != nil {
			t.Fatal(err)
		}
		regenerated, err := (Go{}).WriteDependencies(manifest, dependencies)

		// Assert
		t.Run("it represents requirements and indirect flags with common dependencies", func(t *testing.T) {
			want := []core.Dependency{
				{Name: "example.com/shared", Scope: []string{"require"}, Version: "v1.2.0", Indirect: true},
				{Name: "example.com/unchanged", Scope: []string{"require"}, Version: "v1.0.0"},
			}
			if !reflect.DeepEqual(dependencies, want) {
				t.Fatalf("dependencies = %#v, want %#v", dependencies, want)
			}
		})
		t.Run("it regenerates requirements without losing module settings or comments", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			reparsed, err := (Go{}).ParseDependencies(regenerated)
			if err != nil || !reflect.DeepEqual(reparsed, dependencies) {
				t.Fatalf("round trip changed dependencies: %#v, %v", reparsed, err)
			}
			for _, want := range []string{"// Client module settings", "module example.com/client", "go 1.25.0", "replace example.com/local => ./libs/local", "exclude example.com/blocked v1.0.0"} {
				if !strings.Contains(string(regenerated), want) {
					t.Fatalf("regenerated module lost %q", want)
				}
			}
		})
	})

	for _, scenario := range []struct {
		name, incoming, dependency, wantVersion, wantError string
	}{
		{"when the incoming module adds a dependency", "require example.com/added v1.0.0", "example.com/added", "v1.0.0", ""},
		{"when both modules require the same version", "require example.com/shared v1.2.0", "example.com/shared", "v1.2.0", ""},
		{"when the incoming module requires a newer minor", "require example.com/shared v1.3.0", "example.com/shared", "v1.3.0", ""},
		{"when the client already requires a newer version", "require example.com/shared v1.1.0", "example.com/shared", "v1.2.0", ""},
		{"when the modules require different majors", "require example.com/shared v2.0.0+incompatible", "", "", "incompatible major versions"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			client := readPackageManifestFixture(t, "go.mod")
			incoming := []byte("module example.com/template\ngo 1.25.0\n" + scenario.incoming + "\n")

			// Act: the public merge flow parses both manifests, merges the common
			// dependency lists, and asks the Go backend to write the client module.
			output, err := core.MergeManifestDependencies(Go{}, "go.mod", incoming, client, core.MergeDecisions{}.Resolve)

			// Assert
			if scenario.wantError != "" {
				t.Run("it rejects the conflict without changing either source", func(t *testing.T) {
					if err == nil || !strings.Contains(err.Error(), scenario.wantError) {
						t.Fatalf("error = %v, want %q", err, scenario.wantError)
					}
					if !bytes.Equal(client, readPackageManifestFixture(t, "go.mod")) || output != nil {
						t.Fatal("failed merge changed the source or returned a manifest")
					}
				})
				return
			}
			t.Run("it writes the merged requirements into a valid client module", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				file, err := modfile.Parse("go.mod", output, nil)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, requirement := range file.Require {
					if requirement.Mod.Path == scenario.dependency {
						found = requirement.Mod.Version == scenario.wantVersion && !requirement.Indirect
					}
				}
				if !found || file.Module.Mod.Path != "example.com/client" || len(file.Replace) != 1 || len(file.Exclude) != 1 {
					t.Fatalf("wrong merged module:\n%s", output)
				}
			})
		})
	}
	t.Run("when an incoming go.mod has invalid dependency syntax", func(t *testing.T) {
		// Arrange
		client := readPackageManifestFixture(t, "go.mod")
		incoming := []byte("require ( not a module")

		// Act
		output, err := core.MergeManifestDependencies(Go{}, "go.mod", incoming, client, core.MergeDecisions{}.Resolve)

		// Assert
		t.Run("it rejects parsing before producing a merged manifest", func(t *testing.T) {
			if err == nil || !strings.Contains(err.Error(), "parse shared") || output != nil || !bytes.Equal(client, readPackageManifestFixture(t, "go.mod")) {
				t.Fatalf("invalid dependency syntax was accepted: %s, %v", output, err)
			}
		})
	})

}

func TestGoReleaseConfiguration(t *testing.T) {
	t.Run("when releasing the root Go CLI", func(t *testing.T) {
		root := releaseConfigurationFixture(t, []string{"go"}, false)

		config, err := core.GoReleaserConfig(root)

		t.Run("it preserves the existing binary and archive names", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"project_name: workspace", "binary: workspace", "{{ .ProjectName }}-{{ .Tag }}-", "mode: keep-existing", "replace_existing_artifacts: true"} {
				if !strings.Contains(string(config), want) {
					t.Errorf("release configuration omits %q", want)
				}
			}
		})
	})
	t.Run("when releasing a mixed Go Cargo and Bun workspace", func(t *testing.T) {
		root := releaseConfigurationFixture(t, []string{"go", "cargo", "bun"}, false)

		config, err := core.GoReleaserConfig(root)

		t.Run("it combines the selected providers in one release", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"main: .", "builder: rust", "id: rust-app", "{{ .ProjectName }}-{{ .Tag }}-"} {
				if !strings.Contains(string(config), want) {
					t.Errorf("combined release omits %q", want)
				}
			}
		})
	})
	t.Run("when releasing only libraries", func(t *testing.T) {
		root := releaseConfigurationFixture(t, []string{"go", "cargo", "bun"}, true)

		config, err := core.GoReleaserConfig(root)

		t.Run("it creates a source release without executable builds", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(config), "skip: true") || !strings.Contains(string(config), "disable: true") || strings.Contains(string(config), "binary:") {
				t.Fatalf("expected a source-only release, got %s", config)
			}
		})
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
	manifest.Workspace.Projects = []core.Project{
		{
			Name:     "math",
			Path:     "libs/math",
			Template: "test:math",
			Package: &core.PackageDeclaration{
				Public: boolPointer(true),
			},
		},
		{
			Name:     "cache",
			Path:     "libs/cache",
			Template: "test:cache",
			Package: &core.PackageDeclaration{
				Public: boolPointer(false),
			},
		},
	}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	return root
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
