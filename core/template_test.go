package core

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMergePlanRejectsEmptyDestinationWithoutStaging(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	temp := filepath.Join(root, "temp")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("TMPDIR", temp)

	_, err := BuildMergePlan(MergeRequest{SelectedTemplateRoot: selected})
	if err == nil || !strings.Contains(err.Error(), "merge destination is required") {
		t.Fatalf("error = %v, want required destination error", err)
	}
	if matches, globErr := filepath.Glob(filepath.Join(root, ".premise-stage-*")); globErr != nil || len(matches) != 0 {
		t.Fatalf("merge stages = %v, err = %v", matches, globErr)
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("selected stages = %v", entries)
	}
}

func TestBuildMergePlanLeavesDestinationAbsent(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	plan, err := BuildMergePlan(MergeRequest{SelectedTemplateRoot: selected, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after build: %v", err)
	}
	if _, err := os.Stat(plan.stage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plan.selectedStage, "main.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plan.stage, "main.txt")); !os.IsNotExist(err) {
		t.Fatalf("stage was materialized during build: %v", err)
	}
}

func TestBuildMergePlanPreservesSelectedRootMode(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	destination := filepath.Join(root, "output")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "selected\n", 0o644)
	if err := os.Chmod(selected, 0o750); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildMergePlan(MergeRequest{SelectedTemplateRoot: selected, Destination: destination})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.close()
	if got, want := plan.Root.Mode.Perm(), os.FileMode(0o750); got != want {
		t.Fatalf("planned root mode = %o, want %o", got, want)
	}
	if err := materializeMergePlan(plan, plan.stage); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(plan.stage)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Fatalf("staged root mode = %o, want 750", got)
	}
}

func TestBuildMergePlanRejectsDestinationInsideSource(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := BuildMergePlan(MergeRequest{
		SelectedTemplateRoot: selected,
		Destination:          filepath.Join(selected, "output"),
	})
	if err == nil || !strings.Contains(err.Error(), "inside source") {
		t.Fatalf("error = %v, want destination containment error", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(selected, ".premise-stage-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("source stages = %v, err = %v", matches, globErr)
	}
}

func TestExecuteMergePlanCreatesDestinationAndCleansStages(t *testing.T) {
	installMiseTestShim(t, false)
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(selected, "main.txt"), "orders\n", 0o600)
	plan, err := BuildMergePlan(MergeRequest{SelectedTemplateRoot: selected, Destination: destination, TemplateKind: "app"})
	if err != nil {
		t.Fatal(err)
	}
	stage, selectedStage := plan.stage, plan.selectedStage
	if err := ExecuteMergePlan(context.Background(), plan, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, "main.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage leaked: %v", err)
	}
	if _, err := os.Stat(selectedStage); !os.IsNotExist(err) {
		t.Fatalf("selected stage leaked: %v", err)
	}
}

func TestBuildMergePlanCleansStagesWhenConflictResolutionFails(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "registry", "templates")
	selected := filepath.Join(shared, "app")
	destination := filepath.Join(root, "workspace", "apps", "orders")
	writeTestFile(t, filepath.Join(shared, "NOTICE"), "shared\n", 0o644)
	writeTestFile(t, filepath.Join(selected, "NOTICE"), "selected\n", 0o644)
	if _, err := BuildMergePlan(MergeRequest{
		SharedRegistryRoot: shared, SelectedTemplateRoot: selected, Destination: destination,
		ResolveConflict: func(MergeConflict) (MergeDecision, error) { return MergeDecision{Choice: MergeChoiceAbort}, nil },
	}); err == nil {
		t.Fatal("build succeeded, want conflict abort")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".premise-stage-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("sibling stages = %v, err = %v", matches, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failed build: %v", err)
	}
}
