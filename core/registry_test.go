package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func templateFixture(name, kind string) Template {
	return Template{
		Name:    name,
		Kind:    kind,
		Version: "0.1.0",
		Questions: []Question{{
			Prompt:   kindLabel(kind) + " name:",
			Type:     "string",
			Populate: "name",
		}},
	}
}

// writeRegistryFixture writes a minimal on-disk registry whose manifest declares
// the given templates. Each declared template gets a stub mise.toml so the
// registry round-trips through the same loading path as a real source.
func writeRegistryFixture(t *testing.T, templates ...Template) string {
	t.Helper()
	root := t.TempDir()
	manifest := NewManifest("fixture")
	manifest.Templates = templates
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatalf("save fixture manifest: %v", err)
	}
	for _, template := range templates {
		dir := filepath.Join(root, "templates", template.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create fixture template directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "mise.toml"), []byte("[tasks.build]\nrun = 'echo ok'\n"), 0o644); err != nil {
			t.Fatalf("write fixture template mise.toml: %v", err)
		}
	}
	return root
}

// overrideResolveRepository redirects source resolution to fixture directories.
// It returns a restore function to reinstate the previous loader.
func overrideResolveRepository(fixtures map[string]string) func() {
	original := resolveRepository
	resolveRepository = func(_ context.Context, source string) (string, error) {
		root, ok := fixtures[source]
		if !ok {
			return "", fmt.Errorf("fixture source %q is not registered", source)
		}
		return root, nil
	}
	return func() { resolveRepository = original }
}

func TestRegistryEntrySelector(t *testing.T) {
	entry := RegistryEntry{Source: "cloudvoyant/premise-cargo", Template: templateFixture("premise-rust-lib", "lib")}
	if got, want := entry.Selector(), "cloudvoyant/premise-cargo:premise-rust-lib"; got != want {
		t.Fatalf("Selector() = %q, want %q", got, want)
	}
}

func TestOfficialSourcesIncludesBunRegistry(t *testing.T) {
	const bunSource = "cloudvoyant/premise-bun"
	if !slices.Contains(OfficialSources, bunSource) {
		t.Fatalf("OfficialSources does not include %q", bunSource)
	}
}

func TestDefaultRegistryMergesOfficialSources(t *testing.T) {
	fixtures := make(map[string]string, len(OfficialSources))
	var wantSelectors []string
	for _, source := range OfficialSources {
		prefix := "fixture-" + strings.ReplaceAll(source, "/", "-")
		fixtures[source] = writeRegistryFixture(t,
			templateFixture(prefix+"-z", "app"),
			templateFixture(prefix+"-a", "app"),
		)
		wantSelectors = append(wantSelectors,
			source+":"+prefix+"-a",
			source+":"+prefix+"-z",
		)
	}
	defer overrideResolveRepository(fixtures)()

	entries, err := DefaultRegistry(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Deterministic order: official source order, then template-name order.
	if len(entries) != len(wantSelectors) {
		t.Fatalf("DefaultRegistry returned %d entries, want %d", len(entries), len(wantSelectors))
	}
	for index, want := range wantSelectors {
		if got := entries[index].Selector(); got != want {
			t.Errorf("entries[%d].Selector() = %q, want %q", index, got, want)
		}
	}
	// Labels are human-readable template names when unambiguous.
	for _, entry := range entries {
		if entry.Label != entry.Template.Name {
			t.Errorf("entry %s has unexpected label %q", entry.Selector(), entry.Label)
		}
	}
}

func TestDefaultRegistryDisambiguatesDuplicateNames(t *testing.T) {
	first := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	second := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	bunRegistry := writeRegistryFixture(t)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       first,
		"cloudvoyant/premise-cargo": second,
		"cloudvoyant/premise-bun":   bunRegistry,
	})()

	entries, err := DefaultRegistry(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	// Selectors stay unambiguous even though the template names collide.
	if entries[0].Selector() == entries[1].Selector() {
		t.Fatalf("duplicate name produced ambiguous selectors: %q", entries[0].Selector())
	}
	// Labels carry the source identity so they are distinguishable.
	if entries[0].Label == entries[1].Label {
		t.Fatalf("duplicate name produced indistinguishable labels: %q", entries[0].Label)
	}
	for _, entry := range entries {
		if !strings.Contains(entry.Label, entry.Source) {
			t.Errorf("label %q does not disambiguate by source %q", entry.Label, entry.Source)
		}
	}
}

func TestDefaultRegistryAggregatesLoadErrors(t *testing.T) {
	first := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	bunRegistry := writeRegistryFixture(t)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":     first,
		"cloudvoyant/premise-bun": bunRegistry,
		// premise-cargo intentionally missing so loading fails.
	})()

	_, err := DefaultRegistry(context.Background())
	if err == nil {
		t.Fatal("expected an aggregate error when an official source fails to load")
	}
	if !strings.Contains(err.Error(), "cloudvoyant/premise-cargo") {
		t.Fatalf("aggregate error does not name the failing source: %v", err)
	}
}

func TestResolveOfficialTemplateName(t *testing.T) {
	goRegistry := writeRegistryFixture(t,
		templateFixture("premise-app", "app"),
		templateFixture("premise-lib", "lib"),
	)
	cargoRegistry := writeRegistryFixture(t,
		templateFixture("premise-rust-lib", "lib"),
	)
	bunRegistry := writeRegistryFixture(t,
		templateFixture("premise-commander-cli", "app"),
	)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       goRegistry,
		"cloudvoyant/premise-cargo": cargoRegistry,
		"cloudvoyant/premise-bun":   bunRegistry,
	})()

	tests := []struct {
		name    string
		want    string
		wantErr string
	}{
		{name: "premise-app", want: "cloudvoyant/premise:premise-app"},
		{name: "premise-rust-lib", want: "cloudvoyant/premise-cargo:premise-rust-lib"},
		{name: "premise-commander-cli", want: "cloudvoyant/premise-bun:premise-commander-cli"},
		{name: "missing", wantErr: "not declared by any official registry"},
		{name: "..", wantErr: "invalid template name"},
	}
	for _, test := range tests {
		got, err := ResolveOfficialTemplateName(context.Background(), test.name)
		if test.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("ResolveOfficialTemplateName(%q) error = %v, want containing %q", test.name, err, test.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ResolveOfficialTemplateName(%q): %v", test.name, err)
			continue
		}
		if got != test.want {
			t.Errorf("ResolveOfficialTemplateName(%q) = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestResolveOfficialTemplateNameAmbiguous(t *testing.T) {
	first := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	second := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	bunRegistry := writeRegistryFixture(t)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       first,
		"cloudvoyant/premise-cargo": second,
		"cloudvoyant/premise-bun":   bunRegistry,
	})()

	_, err := ResolveOfficialTemplateName(context.Background(), "premise-app")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	for _, want := range []string{"premise-app", "cloudvoyant/premise", "cloudvoyant/premise-cargo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error does not mention %q: %v", want, err)
		}
	}
}

func TestAskDefaultTemplateReturnsSelectedSelector(t *testing.T) {
	fixtures := make(map[string]string, len(OfficialSources))
	for _, source := range OfficialSources {
		name := "fixture-" + strings.ReplaceAll(source, "/", "-")
		fixtures[source] = writeRegistryFixture(t, templateFixture(name, "app"))
	}
	defer overrideResolveRepository(fixtures)()

	var presented []string
	var selected string
	shuffleCalled := false
	originalShuffle := shuffleRegistryEntries
	shuffleRegistryEntries = func(entries []RegistryEntry) {
		shuffleCalled = true
		slices.Reverse(entries)
	}
	defer func() { shuffleRegistryEntries = originalShuffle }()

	originalPicker := promptPickEntry
	promptPickEntry = func(entries []RegistryEntry) (string, error) {
		if len(entries) == 0 {
			return "", fmt.Errorf("picker received no entries")
		}
		for _, entry := range entries {
			presented = append(presented, entry.Selector())
		}
		selected = entries[len(entries)-1].Selector()
		return selected, nil
	}
	defer func() { promptPickEntry = originalPicker }()

	selector, err := AskDefaultTemplate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !shuffleCalled {
		t.Fatal("AskDefaultTemplate did not shuffle the combined registry")
	}
	if selector != selected {
		t.Fatalf("AskDefaultTemplate returned %q, want %q", selector, selected)
	}
	if len(presented) != len(OfficialSources) {
		t.Fatalf("picker enumerated %d entries, want %d", len(presented), len(OfficialSources))
	}
	lastSource := OfficialSources[len(OfficialSources)-1]
	wantFirst := lastSource + ":fixture-" + strings.ReplaceAll(lastSource, "/", "-")
	if presented[0] != wantFirst {
		t.Fatalf("first picker entry = %q, want shuffled entry %q", presented[0], wantFirst)
	}
}

func TestAskRegistryTemplateScopesToSource(t *testing.T) {
	goRegistry := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	cargoRegistry := writeRegistryFixture(t,
		templateFixture("premise-rust-lib", "lib"),
		templateFixture("premise-rust-app", "app"),
	)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       goRegistry,
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	var presented []string
	originalPicker := promptPickEntry
	promptPickEntry = func(entries []RegistryEntry) (string, error) {
		for _, entry := range entries {
			presented = append(presented, entry.Selector())
		}
		return "cloudvoyant/premise-cargo:premise-rust-lib", nil
	}
	defer func() { promptPickEntry = originalPicker }()

	selector, err := AskRegistryTemplate(context.Background(), "cloudvoyant/premise-cargo")
	if err != nil {
		t.Fatal(err)
	}
	if selector != "cloudvoyant/premise-cargo:premise-rust-lib" {
		t.Fatalf("AskRegistryTemplate returned %q", selector)
	}
	if len(presented) != 2 {
		t.Fatalf("picker enumerated %d entries, want 2 cargo templates only", len(presented))
	}
	for _, entry := range presented {
		if !strings.HasPrefix(entry, "cloudvoyant/premise-cargo:") {
			t.Errorf("picker leaked a non-cargo entry: %q", entry)
		}
	}
}

func TestParseTemplateSelector(t *testing.T) {
	tests := []struct {
		selector string
		source   string
		name     string
		local    bool
	}{
		{selector: ":premise-app", source: NativeTemplateSource, name: "premise-app"},
		{selector: ":app", source: NativeTemplateSource, name: "app"},
		{selector: ".:lib", source: ".", name: "lib", local: true},
		{selector: "cloudvoyant/premise-template:app", source: "cloudvoyant/premise-template", name: "app"},
		{selector: "https://github.com/cloudvoyant/premise-template.git:lib", source: "https://github.com/cloudvoyant/premise-template.git", name: "lib"},
	}
	for _, test := range tests {
		selection, err := ParseTemplateSelector(test.selector)
		if err != nil {
			t.Fatalf("%s: %v", test.selector, err)
		}
		if selection.Source != test.source || selection.Name != test.name || selection.Local != test.local {
			t.Errorf("%s: unexpected selection %#v", test.selector, selection)
		}
	}
	for _, selector := range []string{"app", ":", ":../app"} {
		if _, err := ParseTemplateSelector(selector); err == nil {
			t.Errorf("expected %q to be rejected", selector)
		}
	}
}

func TestClassifyGenerateSelector(t *testing.T) {
	tests := []struct {
		argument string
		kind     GenerateSelectorKind
		value    string
	}{
		{argument: "", kind: GenerateSelectorDefault},
		{argument: ":premise-rust-lib", kind: GenerateSelectorOfficialName, value: "premise-rust-lib"},
		{argument: "cloudvoyant/premise-cargo", kind: GenerateSelectorSource, value: "cloudvoyant/premise-cargo"},
		{argument: "cloudvoyant/premise:premise-app", kind: GenerateSelectorExplicit, value: "cloudvoyant/premise:premise-app"},
		{argument: ".:app", kind: GenerateSelectorExplicit, value: ".:app"},
		{argument: "../templates:lib", kind: GenerateSelectorExplicit, value: "../templates:lib"},
		{argument: "https://github.com/cloudvoyant/premise-template.git:app", kind: GenerateSelectorExplicit, value: "https://github.com/cloudvoyant/premise-template.git:app"},
	}
	for _, test := range tests {
		classified, err := ClassifyGenerateSelector(test.argument)
		if err != nil {
			t.Fatalf("ClassifyGenerateSelector(%q): %v", test.argument, err)
		}
		if classified.Kind != test.kind || classified.Value != test.value {
			t.Errorf("ClassifyGenerateSelector(%q) = %#v, want kind %v value %q", test.argument, classified, test.kind, test.value)
		}
	}

	for _, test := range []struct{ selector, want string }{
		{selector: ":", want: "invalid template name"},
		{selector: ":bad name", want: "invalid template name"},
		{selector: "cloudvoyant/premise:", want: "must end with :<template>"},
		{selector: "cloudvoyant/premise:bad name", want: "invalid template name"},
	} {
		_, err := ClassifyGenerateSelector(test.selector)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("ClassifyGenerateSelector(%q) error = %v, want containing %q", test.selector, err, test.want)
		}
	}
}

func TestRegistryResolutionRecordsQualifiedProvenance(t *testing.T) {
	installMiseTestShim(t, false)
	goRegistry := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	cargoRegistry := writeRegistryFixture(t, templateFixture("premise-rust-lib", "lib"))
	bunRegistry := writeRegistryFixture(t)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       goRegistry,
		"cloudvoyant/premise-cargo": cargoRegistry,
		"cloudvoyant/premise-bun":   bunRegistry,
	})()

	tests := []struct {
		name    string
		resolve func(*testing.T, context.Context) string
	}{
		{
			name: "bare name across official registries",
			resolve: func(t *testing.T, ctx context.Context) string {
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
			workspace := filepath.Join(t.TempDir(), "workspace")
			if _, err := InitializeWorkspace(workspace, "[tasks.build]\nrun = 'echo ok'\n"); err != nil {
				t.Fatal(err)
			}
			if err := Generate(context.Background(), workspace, selector, fixedGenerateOptions(fixedQuestionnaire{"name": "orders"}), io.Discard); err != nil {
				t.Fatal(err)
			}
			manifest, err := LoadManifest(filepath.Join(workspace, ManifestFilename))
			if err != nil {
				t.Fatal(err)
			}
			if project := manifest.Workspace.Projects[0]; project.Template != selector {
				t.Fatalf("recorded template = %q, want %q", project.Template, selector)
			}
		})
	}
}

func TestDetectProjectKind(t *testing.T) {
	write := func(t *testing.T, kind ProjectKind, templates []Template, mise string) string {
		t.Helper()
		root := t.TempDir()
		manifest := NewManifest("fixture")
		manifest.Workspace.Kind = kind
		manifest.Templates = templates
		if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
			t.Fatal(err)
		}
		if mise != "" {
			if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte(mise), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}

	for _, test := range []struct {
		name    string
		root    string
		want    ProjectKind
		wantErr string
	}{
		{name: "inferred template registry", root: write(t, "", []Template{templateFixture("app", "app")}, ""), want: ProjectKindTemplateRegistry},
		{name: "declared empty template registry", root: write(t, ProjectKindTemplateRegistry, nil, ""), want: ProjectKindTemplateRegistry},
		{name: "declared monorepo", root: write(t, ProjectKindMonorepo, nil, "monorepo_root = true\n"), want: ProjectKindMonorepo},
		{name: "declared monorepo missing marker", root: write(t, ProjectKindMonorepo, nil, "[tools]\nnode = 'lts'\n"), wantErr: "requires top-level"},
		{name: "hybrid", root: write(t, "", []Template{templateFixture("app", "app")}, "monorepo_root = true\n"), wantErr: "cannot be both"},
		{name: "nested marker is not monorepo", root: write(t, "", nil, "[env]\nmonorepo_root = true\n"), wantErr: "must be either"},
		{name: "unclassified", root: write(t, "", nil, ""), wantErr: "must be either"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := DetectProjectKind(test.root)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("DetectProjectKind() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("DetectProjectKind() = %q, want %q", got, test.want)
			}
		})
	}
}
