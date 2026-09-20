package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedScaffoldLifecycleDefersDestinationWritesUntilCommit(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "registry", "templates")
	selected := filepath.Join(shared, "app")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(shared, ".gitignore"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, ".gitignore"), "selected\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "main.txt"), "premise-app\n", 0o600)

	prepared, err := PrepareScaffold(ScaffoldRequest{
		SharedSource: shared,
		Source:       selected,
		Destination:  destination,
		Replacements: map[string]string{"premise-app": "orders", "orders": "wrong-second-pass"},
		Resolver:     &mapMergeResolver{Decisions: map[string]MergeDecision{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })

	if filepath.Dir(prepared.Stage) != filepath.Dir(destination) {
		t.Fatalf("stage %s is not a sibling of destination %s", prepared.Stage, destination)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists before commit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(prepared.Stage, "main.txt")); !os.IsNotExist(err) {
		t.Fatalf("stage was materialized during prepare: %v", err)
	}
	selectedBytes, err := os.ReadFile(filepath.Join(prepared.SelectedStage, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(selectedBytes); got != "orders\n" {
		t.Fatalf("selected staging substitution = %q, want one substitution pass", got)
	}

	if err := prepared.Materialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists before commit: %v", err)
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if prepared.Stage != "" {
		t.Fatalf("committed stage still owned: %q", prepared.Stage)
	}
	generated, err := os.ReadFile(filepath.Join(destination, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(generated); got != "orders\n" {
		t.Fatalf("generated main.txt = %q, want one substitution pass", got)
	}
	if info, err := os.Stat(filepath.Join(destination, "main.txt")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("generated mode = %v, err = %v", info.Mode().Perm(), err)
	}
	selectedStage := prepared.SelectedStage
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(selectedStage); !os.IsNotExist(err) {
		t.Fatalf("selected stage leaked: %v", err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestPreparedScaffoldCommitPreservesSelectedRootMode(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "registry", "templates", "app")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "selected\n", 0o644)
	if err := os.Chmod(selected, 0o750); err != nil {
		t.Fatal(err)
	}

	prepared, err := PrepareScaffold(ScaffoldRequest{
		Source:      selected,
		Destination: destination,
		Resolver:    &mapMergeResolver{Decisions: map[string]MergeDecision{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if got := prepared.Plan.Root.Mode.Perm(); got != 0o750 {
		t.Fatalf("planned root mode = %o, want 750", got)
	}
	if err := prepared.Materialize(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Fatalf("destination mode = %o, want 750", got)
	}
}

func TestPreparedScaffoldCommitRejectsDestinationCreatedAfterPrepare(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "registry", "templates", "app")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "selected\n", 0o644)
	prepared, err := PrepareScaffold(ScaffoldRequest{
		Source:      selected,
		Destination: destination,
		Resolver:    &mapMergeResolver{Decisions: map[string]MergeDecision{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if err := prepared.Materialize(); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(destination, "owner.txt"), "external\n", 0o644)
	if err := prepared.Commit(); err == nil {
		t.Fatal("Commit succeeded after destination appeared")
	}
	content, err := os.ReadFile(filepath.Join(destination, "owner.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "external\n" {
		t.Fatalf("destination was changed: %q", content)
	}
	if _, err := os.Stat(prepared.Stage); err != nil {
		t.Fatalf("uncommitted stage disappeared before Close: %v", err)
	}
}

func TestPrepareScaffoldCleansStagesWhenConflictResolutionFails(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "registry", "templates")
	selected := filepath.Join(shared, "app")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o644)

	selectedTemp := filepath.Join(root, "selected-temp")
	if err := os.MkdirAll(selectedTemp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", selectedTemp)
	_, err := PrepareScaffold(ScaffoldRequest{
		SharedSource: shared,
		Source:       selected,
		Destination:  destination,
		Resolver:     &mapMergeResolver{Decisions: map[string]MergeDecision{}},
	})
	if err == nil {
		t.Fatal("PrepareScaffold succeeded, want merge abort")
	}
	if matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(destination), ".premise-stage-*")); globErr != nil || len(matches) != 0 {
		t.Fatalf("sibling stages = %v, err = %v", matches, globErr)
	}
	if entries, readErr := os.ReadDir(selectedTemp); readErr != nil || len(entries) != 0 {
		t.Fatalf("selected stages = %v, err = %v", entries, readErr)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after failed prepare: %v", statErr)
	}
}
