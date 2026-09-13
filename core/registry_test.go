package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func TestDefaultRegistryMergesOfficialSources(t *testing.T) {
	goRegistry := writeRegistryFixture(t,
		templateFixture("premise-app", "app"),
		templateFixture("premise-lib", "lib"),
	)
	cargoRegistry := writeRegistryFixture(t,
		templateFixture("premise-rust-lib", "lib"),
		templateFixture("premise-rust-app", "app"),
		templateFixture("premise-clap-cli", "app"),
		templateFixture("premise-ratatui-app", "app"),
	)
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       goRegistry,
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	entries, err := DefaultRegistry(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Deterministic order: official source order, then template-name order.
	wantSelectors := []string{
		"cloudvoyant/premise:premise-app",
		"cloudvoyant/premise:premise-lib",
		"cloudvoyant/premise-cargo:premise-clap-cli",
		"cloudvoyant/premise-cargo:premise-ratatui-app",
		"cloudvoyant/premise-cargo:premise-rust-app",
		"cloudvoyant/premise-cargo:premise-rust-lib",
	}
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
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       first,
		"cloudvoyant/premise-cargo": second,
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
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise": first,
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
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       goRegistry,
		"cloudvoyant/premise-cargo": cargoRegistry,
	})()

	tests := []struct {
		name    string
		want    string
		wantErr string
	}{
		{name: "premise-app", want: "cloudvoyant/premise:premise-app"},
		{name: "premise-rust-lib", want: "cloudvoyant/premise-cargo:premise-rust-lib"},
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
	defer overrideResolveRepository(map[string]string{
		"cloudvoyant/premise":       first,
		"cloudvoyant/premise-cargo": second,
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
	goRegistry := writeRegistryFixture(t, templateFixture("premise-app", "app"))
	cargoRegistry := writeRegistryFixture(t, templateFixture("premise-rust-lib", "lib"))
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
		return entries[1].Selector(), nil
	}
	defer func() { promptPickEntry = originalPicker }()

	selector, err := AskDefaultTemplate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if selector != "cloudvoyant/premise-cargo:premise-rust-lib" {
		t.Fatalf("AskDefaultTemplate returned %q", selector)
	}
	if len(presented) != 2 {
		t.Fatalf("picker enumerated %d entries, want 2", len(presented))
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
