package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type mapMergeResolver struct {
	Decisions map[string]MergeDecision
	Calls     []MergeConflict
}

func (resolver *mapMergeResolver) ResolveMergeConflict(conflict MergeConflict) (MergeDecision, error) {
	resolver.Calls = append(resolver.Calls, conflict)
	if decision, ok := resolver.Decisions[conflict.Key]; ok {
		return decision, nil
	}
	if decision, ok := resolver.Decisions[conflict.Path]; ok {
		return decision, nil
	}
	return MergeDecision{Choice: MergeChoiceAbort}, nil
}

func TestMergeOrderedPolicyPreservesPrecedenceAndAdjacentDuplicates(t *testing.T) {
	shared := []byte("# shared\r\ncache/\r\ncache/\r\n!cache/keep\r\nrepeat\r\n")
	selected := []byte("repeat\n# selected\nrepeat\n")
	want := "# shared\ncache/\n!cache/keep\nrepeat\n# selected\nrepeat\n"
	if got := string(mergeOrderedLines(shared, selected)); got != want {
		t.Fatalf("mergeOrderedLines() = %q, want %q", got, want)
	}
}

func TestBuildMergePlanSortsAndResolvesFocusedCollisions(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "templates")
	selected := filepath.Join(shared, "app")
	if err := os.MkdirAll(filepath.Join(selected, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(shared, ".gitignore"), "shared-placeholder\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o640)
	writeTestFile(t, filepath.Join(selected, ".gitignore"), "!shared-placeholder\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o600)
	writeTestFile(t, filepath.Join(selected, "main.txt"), "hello shared-placeholder\n", 0o644)
	writeTestFile(t, filepath.Join(shared, "sibling", "ignored.txt"), "ignored\n", 0o644)
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{"NOTICE": {Choice: MergeChoiceKeepShared}}}
	destination := filepath.Join(root, "output")

	plan, err := BuildMergePlan(MergeRequest{SharedRegistryRoot: shared, SelectedTemplateRoot: selected, Destination: destination, Substitutions: map[string]string{"shared-placeholder": "orders"}, TemplateKind: "app", SelectedIdentity: "app", ResolveConflict: resolver.ResolveMergeConflict})
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(plan.Entries))
	for index, entry := range plan.Entries {
		paths[index] = entry.Path
	}
	wantPaths := []string{".gitignore", "NOTICE", "empty", "main.txt"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	if got := string(plan.Entries[0].Output.Data); got != "orders\n!orders\n" {
		t.Fatalf("merged .gitignore = %q", got)
	}
	if plan.Entries[0].Strategy != MergeStrategyOrderedLines || plan.Entries[1].Strategy != MergeStrategyWholeFile {
		t.Fatalf("unexpected strategies: %#v", plan.Entries)
	}
	if got := string(plan.Entries[1].Output.Data); got != "shared\n" || plan.Entries[1].Output.Mode.Perm() != 0o640 {
		t.Fatalf("NOTICE output = %q mode %o", got, plan.Entries[1].Output.Mode.Perm())
	}

	if err := materializeMergePlan(plan, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "hello orders\n" {
		t.Fatalf("main.txt = %q", got)
	}
	if info, err := os.Stat(filepath.Join(destination, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory missing: %v", err)
	}
}

func TestBuildMergePlanPrunesSelectedDirectoryWhenSharedFileWins(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(shared, "config"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "config", "nested.txt"), "selected\n", 0o644)
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{
		"config": {Choice: MergeChoiceKeepShared},
	}}
	destination := filepath.Join(root, "output")

	plan, err := BuildMergePlan(MergeRequest{SharedRegistryRoot: shared, SelectedTemplateRoot: selected, Destination: destination, SelectedIdentity: "selected", ResolveConflict: resolver.ResolveMergeConflict})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(plan.Entries), 1; got != want {
		t.Fatalf("entries = %#v, want %d entry", plan.Entries, want)
	}
	if entry := plan.Entries[0]; entry.Path != "config" || entry.Output.Kind != "file" {
		t.Fatalf("config entry = %#v", entry)
	}

	if err := materializeMergePlan(plan, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != "shared\n" {
		t.Fatalf("config = %q, want shared", got)
	}
}

func TestBuildMergePlanDoesNotCoalesceEqualBytesWithDifferentModes(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	selected := filepath.Join(root, "selected")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(shared, "same"), "same\n", 0o600)
	writeTestFile(t, filepath.Join(selected, "same"), "same\n", 0o644)
	resolver := &mapMergeResolver{Decisions: map[string]MergeDecision{"same": {Choice: MergeChoiceUseSelected}}}
	destination := filepath.Join(root, "output")
	plan, err := BuildMergePlan(MergeRequest{SharedRegistryRoot: shared, SelectedTemplateRoot: selected, Destination: destination, SelectedIdentity: "selected", ResolveConflict: resolver.ResolveMergeConflict})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Entries[0].Strategy != MergeStrategyWholeFile || plan.Entries[0].Output.Mode.Perm() != 0o644 {
		t.Fatalf("equal bytes with differing modes were silently coalesced: %#v", plan.Entries[0])
	}
	if len(resolver.Calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1", len(resolver.Calls))
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
