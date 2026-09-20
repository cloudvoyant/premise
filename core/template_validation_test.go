package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateToolChangesRunsEachChangeInDisposableCopy(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n", 0o644)
	log := installMiseTestShim(t, false)
	if err := ValidateToolChanges(context.Background(), selected, "lib", []ToolChange{{Name: "bun", From: "1.1", To: "1.2"}}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	tasks, err := ContractTasks("lib")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(lines), 1+len(tasks); got != want {
		t.Fatalf("mise calls = %d, want %d:\n%s", got, want, data)
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || parts[1] != "1" || parts[0] == selected {
			t.Fatalf("unexpected isolated invocation %q", line)
		}
	}
	original, err := os.ReadFile(filepath.Join(selected, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != "[tools]\nbun = '1.1'\n" {
		t.Fatalf("selected template mutated:\n%s", original)
	}
}

func TestValidateToolChangesAppliesCompleteStructuredValue(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nnode = { version = '20', os = ['linux'] }\n", 0o644)
	captured := filepath.Join(t.TempDir(), "mise.toml")
	t.Setenv("MISE_CONFIG_LOG", captured)
	installMiseTestShim(t, false)
	change := ToolChange{
		Name: "node",
		From: map[string]any{"version": "20", "os": []any{"linux"}},
		To:   map[string]any{"version": "22", "os": []any{"linux", "macos"}},
	}
	if err := ValidateToolChanges(context.Background(), selected, "lib", []ToolChange{change}, os.Stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	config, err := ExtractMiseConfig("captured preflight", data)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Tools["node"].Raw; !reflect.DeepEqual(got, change.To) {
		t.Fatalf("preflight node = %#v, want %#v", got, change.To)
	}
}

func TestValidateToolChangesAttributesFailureAndCleansCopy(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n", 0o644)
	temp := filepath.Join(t.TempDir(), "preflights")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	installMiseTestShim(t, true)
	err := ValidateToolChanges(context.Background(), selected, "lib", []ToolChange{{Name: "bun", From: "1.1", To: "1.2"}}, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "tool update bun 1.1 -> 1.2 failed") {
		t.Fatalf("error = %v", err)
	}
	entries, readErr := os.ReadDir(temp)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("preflight copies leaked: %v", entries)
	}
}

func TestValidateGeneratedCandidateRunsContractsInDisposableCopy(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(stage, "mise.toml"), "[tasks.build]\nrun = 'echo build'\n", 0o644)
	writeTestFile(t, filepath.Join(stage, "marker.txt"), "unchanged\n", 0o600)
	temporary := filepath.Join(t.TempDir(), "candidates")
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temporary)
	log := installMiseTestShim(t, false)

	if err := ValidateGeneratedCandidate(context.Background(), stage, "app", os.Stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	tasks, err := ContractTasks("app")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(lines), 1+len(tasks); got != want {
		t.Fatalf("mise calls = %d, want %d:\n%s", got, want, data)
	}
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || parts[1] != "1" || parts[0] == stage || !strings.Contains(parts[0], "premise-candidate-validation-") {
			t.Fatalf("unexpected candidate invocation %q", line)
		}
	}
	assertDirectoryEmpty(t, temporary)
	marker, err := os.ReadFile(filepath.Join(stage, "marker.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != "unchanged\n" {
		t.Fatalf("stage changed: %q", marker)
	}
}

func TestValidateGeneratedCandidateCleansCopyOnFailure(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(stage, "mise.toml"), "[tasks.build]\nrun = 'echo build'\n", 0o644)
	temporary := filepath.Join(t.TempDir(), "candidates")
	if err := os.MkdirAll(temporary, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temporary)
	installMiseTestShim(t, true)

	err := ValidateGeneratedCandidate(context.Background(), stage, "lib", os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "generated candidate validation failed") {
		t.Fatalf("error = %v", err)
	}
	assertDirectoryEmpty(t, temporary)
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary directories leaked in %s: %v", path, entries)
	}
}

func installMiseTestShim(t *testing.T, fail bool) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "mise.log")
	script := "#!/bin/sh\nprintf '%s|%s|%s\\n' \"$PWD\" \"$PREMISE_TEMPLATE_TEST\" \"$*\" >> \"$MISE_LOG\"\nif [ -n \"$MISE_CONFIG_LOG\" ] && [ ! -e \"$MISE_CONFIG_LOG\" ]; then cp \"$PWD/mise.toml\" \"$MISE_CONFIG_LOG\"; fi\n"
	if fail {
		script += "exit 7\n"
	} else {
		script += "exit 0\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISE_LOG", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}
