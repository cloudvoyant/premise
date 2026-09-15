package core

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolutionPathsRecordQualifiedProvenance drives the new selector
// resolution paths end to end against fixture registries, using the
// resolveRepository and promptPickEntry seams so nothing touches the network.
// Each path must yield the fully qualified cloudvoyant/premise-cargo:<name>
// selector, and Generate must record that exact selector as the project's
// provenance rather than the bare or source-only form the user typed.
func TestResolutionPathsRecordQualifiedProvenance(t *testing.T) {
	goRegistry := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	cargoRegistry := writeRegistryFixture(t, templateFixture("premise-rust-lib", "lib"))
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       goRegistry,
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	tests := []struct {
		name    string
		resolve func(*testing.T, context.Context) string
	}{
		{
			name: "bare name across official registries",
			resolve: func(t *testing.T, ctx context.Context) string {
				// The CLI strips the leading ":" before resolving a bare name.
				selector, err := ResolveOfficialTemplateName(ctx, "premise-rust-lib")
				if err != nil {
					t.Fatal(err)
				}
				return selector
			},
		},
		{
			name: "source-only scoped picker",
			resolve: func(t *testing.T, ctx context.Context) string {
				original := promptPickEntry
				promptPickEntry = func(entries []RegistryEntry) (string, error) {
					if len(entries) != 1 {
						t.Fatalf("scoped picker received %d entries, want 1", len(entries))
					}
					if entries[0].Selector() != "cloudvoyant/premise-cargo:premise-rust-lib" {
						t.Fatalf("scoped picker received %q", entries[0].Selector())
					}
					return entries[0].Selector(), nil
				}
				defer func() { promptPickEntry = original }()

				selector, err := AskRegistryTemplate(ctx, "cloudvoyant/premise-cargo")
				if err != nil {
					t.Fatal(err)
				}
				return selector
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selector := test.resolve(t, context.Background())
			if selector != "cloudvoyant/premise-cargo:premise-rust-lib" {
				t.Fatalf("resolved selector = %q, want cloudvoyant/premise-cargo:premise-rust-lib", selector)
			}

			workspace := filepath.Join(t.TempDir(), "workspace")
			if _, err := InitializeWorkspace(workspace, "[tasks.build]\nrun = 'echo ok'\n"); err != nil {
				t.Fatal(err)
			}
			if err := Generate(context.Background(), workspace, selector, fixedQuestionnaire{"name": "orders"}, io.Discard); err != nil {
				t.Fatal(err)
			}

			manifest, err := LoadManifest(filepath.Join(workspace, ManifestFilename))
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.Workspace.Projects) != 1 {
				t.Fatalf("got %d projects, want 1", len(manifest.Workspace.Projects))
			}
			if project := manifest.Workspace.Projects[0]; project.Template != selector {
				t.Fatalf("recorded template = %q, want %q", project.Template, selector)
			}
		})
	}
}

// TestBareNameResolutionAmbiguityErrors pins the zero- and multiple-match errors
// the bare-name resolution path reports, so callers are always asked to qualify
// the source rather than silently picking a single match.
func TestBareNameResolutionAmbiguityErrors(t *testing.T) {
	goRegistry := writeRegistryFixture(t,
		templateFixture("premise-app", "app"),
		templateFixture("shared", "app"),
	)
	cargoRegistry := writeRegistryFixture(t,
		templateFixture("premise-rust-lib", "lib"),
		templateFixture("shared", "lib"),
	)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       goRegistry,
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	if _, err := ResolveOfficialTemplateName(context.Background(), "missing"); err == nil ||
		!strings.Contains(err.Error(), "not declared by any official registry") {
		t.Fatalf("zero-match error = %v", err)
	}

	if _, err := ResolveOfficialTemplateName(context.Background(), "shared"); err == nil {
		t.Fatal("expected an ambiguity error")
	} else {
		for _, want := range []string{"shared", "cloudvoyant/premise", "cloudvoyant/premise-cargo"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("ambiguity error does not mention %q: %v", want, err)
			}
		}
	}
}
