package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

func TestDeploymentFansOutMigrationsAndRestoresPreviousArtifactsAfterFailedHealth(t *testing.T) {
	// Arrange.
	fixture := prepareLocalDeployment(t, true)
	plan := fixture.plan(t)
	if len(plan.Migrations) != 2 {
		t.Fatalf("expected only api and worker migrations, got %v", plan.Migrations)
	}
	fixture.phase(t, "start", "release-a", fixture.releaseArguments("A")...)
	if output := fixture.expectFailure(t, "start", "competing", fixture.releaseArguments("B")...); !strings.Contains(output, "lock") {
		t.Fatalf("overlapping deployment did not report ownership: %s", output)
	}
	fixture.phase(t, "provision", "release-a")
	if output := fixture.expectInfraFailure(t, "apply"); !strings.Contains(output, "environment lock") {
		t.Fatalf("standalone apply bypassed deployment ownership: %s", output)
	}
	barrier := t.TempDir()
	t.Setenv("MIGRATION_BARRIER", barrier)

	// Act: run independent jobs as separate CLI processes, then complete release A.
	commands := make([]*exec.Cmd, 0, len(plan.Migrations))
	for _, group := range plan.Migrations {
		command := exec.CommandContext(t.Context(), fixture.binary, fixture.arguments("migrate", "release-a", "--group", group.Name)...)
		command.Dir = fixture.root
		command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+fixture.root)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, command)
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MIGRATION_BARRIER", "")
	fixture.phase(t, "apply", "release-a")
	fixture.phase(t, "complete", "release-a")
	fixture = fixture.freshCheckout(t)
	t.Setenv("FAIL_HEALTH_APP", "api")
	output := fixture.expectFailure(t, "run", "release-b", fixture.releaseArguments("B")...)

	// Assert: B ran and migrated, automatic recovery serves A without reverting schema.
	if !strings.Contains(output, "injected health failure after serving release B") {
		t.Fatalf("wrong deployment failure: %s", output)
	}
	if _, err := os.Stat(filepath.Join(fixture.state, "api-observed-B")); err != nil {
		t.Fatal("health check never observed the deployed B artifact")
	}
	for _, app := range []string{"api", "worker", "website"} {
		body := fixture.application(t, app)
		if body["release"] != "A" {
			t.Fatalf("%s serves %s after rollback", app, body["release"])
		}
		if app != "website" && body["message"] != "preserved-row" {
			t.Fatalf("%s lost database data", app)
		}
	}
	connection := strings.TrimSpace(infraCommand(t, fixture.root, fixture.binary, "infra", "query", "--env", "preview", "--branch", fixture.branch, "--output", "apiDatabaseUrl", "--show-secrets"))
	if actual := strings.TrimSpace(infraCommand(t, fixture.root, "psql", connection, "-qAt", "-c", "SELECT count(*) FROM information_schema.columns WHERE table_name='messages' AND column_name='detail'")); actual != "1" {
		t.Fatal("rollback removed the backward-compatible B schema")
	}
	if actual := strings.TrimSpace(infraCommand(t, fixture.root, "psql", connection, "-qAt", "-c", "SELECT string_agg(version, ',' ORDER BY version) FROM schema_versions")); actual != "A,B" {
		t.Fatalf("rollback changed migration history: %s", actual)
	}
	var record core.ReleaseRecord
	path := filepath.Join(strings.TrimPrefix(plan.RecordsURL, "file://"), "last-success.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Release != "A" {
		t.Fatalf("failed release replaced successful recovery inputs: %s", record.Release)
	}
}

func TestPartialInfrastructureFailureRestoresEveryApplicationAndPreservesMigrations(t *testing.T) {
	// Arrange.
	fixture := prepareLocalDeployment(t, true)
	fixture.phase(t, "run", "release-a", fixture.releaseArguments("A")...)
	t.Setenv("FAIL_UPDATE_APP", "website")

	// Act.
	output := fixture.expectFailure(t, "run", "release-b", fixture.releaseArguments("B")...)

	// Assert.
	if !strings.Contains(output, "injected application update failure") {
		t.Fatalf("wrong infrastructure failure: %s", output)
	}
	if _, err := os.Stat(filepath.Join(fixture.state, "partial-update-observed")); err != nil {
		t.Fatal("failure did not occur after a partial application update")
	}
	for _, app := range []string{"api", "worker", "website"} {
		if fixture.application(t, app)["release"] != "A" {
			t.Fatalf("partial infrastructure failure left %s on B:\n%s", app, output)
		}
	}
	connection := strings.TrimSpace(infraCommand(t, fixture.root, fixture.binary, "infra", "query", "--env", "preview", "--branch", fixture.branch, "--output", "apiDatabaseUrl", "--show-secrets"))
	if actual := strings.TrimSpace(infraCommand(t, fixture.root, "psql", connection, "-qAt", "-c", "SELECT string_agg(version, ',' ORDER BY version) FROM schema_versions")); actual != "A,B" {
		t.Fatalf("failed update lost migration history: %s", actual)
	}
}

func TestSingleMigratingApplicationRunsWithoutAMigrationTaskOnItsPeer(t *testing.T) {
	// Arrange.
	fixture := prepareLocalDeployment(t, true)
	path := filepath.Join(fixture.root, core.ManifestFilename)
	manifest, err := core.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Deployment.Applications = append(manifest.Workspace.Deployment.Applications[:1], manifest.Workspace.Deployment.Applications[2])
	manifest.Workspace.Infra.Prerequisites = []string{"*::api-database"}
	if err := core.SaveManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	delete(fixture.artifacts["A"], "worker")

	// Act.
	plan := fixture.plan(t)
	fixture.phase(t, "run", "release-a", fixture.releaseArguments("A")...)

	// Assert.
	if len(plan.Migrations) != 1 || plan.Migrations[0].Name != "api" {
		t.Fatalf("expected only api migration, got %v", plan.Migrations)
	}
	for _, app := range []string{"api", "website"} {
		if fixture.application(t, app)["release"] != "A" {
			t.Fatalf("%s did not deploy", app)
		}
	}
}

func TestSharedMigrationGroupRunsInOrderAcrossFreshPhaseCheckouts(t *testing.T) {
	// Arrange.
	fixture := prepareLocalDeployment(t, true)
	manifestPath := filepath.Join(fixture.root, core.ManifestFilename)
	manifest, err := core.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Deployment.MigrationGroups = map[string][]string{"shared-history": {"api", "worker"}}
	if err := core.SaveManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	order := filepath.Join(t.TempDir(), "migration-order")
	t.Setenv("MIGRATION_ORDER", order)
	plan := fixture.plan(t)
	if len(plan.Migrations) != 1 || plan.Migrations[0].Name != "shared-history" {
		t.Fatalf("shared history produced independent jobs: %v", plan.Migrations)
	}

	// Act: each later phase reads state and run metadata from another checkout.
	fixture.phase(t, "start", "release-a", fixture.releaseArguments("A")...)
	fixture.phase(t, "provision", "release-a")
	fixture = fixture.freshCheckout(t)
	fixture.phase(t, "migrate", "release-a", "--group", "shared-history")
	fixture = fixture.freshCheckout(t)
	fixture.phase(t, "apply", "release-a")
	fixture.phase(t, "complete", "release-a")

	// Assert.
	actual, err := os.ReadFile(order)
	if err != nil || string(actual) != "api\nworker\n" {
		t.Fatalf("shared migration order = %q, error %v", actual, err)
	}
	if fixture.application(t, "api")["release"] != "A" {
		t.Fatal("fresh-runner deployment did not serve A")
	}
}

func TestMigrationFailureKeepsPreviousApplicationAndPreventsFullUpdate(t *testing.T) {
	// Arrange.
	fixture := prepareLocalDeployment(t, true)
	fixture.phase(t, "run", "release-a", fixture.releaseArguments("A")...)
	t.Setenv("FAIL_MIGRATION_APP", "api")

	// Act.
	output := fixture.expectFailure(t, "run", "release-b", fixture.releaseArguments("B")...)

	// Assert.
	if !strings.Contains(output, "injected migration failure") {
		t.Fatalf("wrong failure: %s", output)
	}
	if fixture.application(t, "api")["release"] != "A" {
		t.Fatal("a failed migration updated the application")
	}
	if _, err := os.Stat(filepath.Join(fixture.state, "api-observed-B")); !os.IsNotExist(err) {
		t.Fatal("health checks ran after a failed migration")
	}
}

func TestDeploymentWithoutMigrationTasksAppliesAndCompletesWithoutMigrationJobs(t *testing.T) {
	// Arrange.
	fixture := prepareLocalDeployment(t, false)
	plan := fixture.plan(t)

	// Act.
	fixture.phase(t, "run", "release-a", fixture.releaseArguments("A")...)

	// Assert.
	if len(plan.Migrations) != 0 || len(plan.MigrationMatrix.Include) != 0 {
		t.Fatalf("non-migrating application produced jobs: %v", plan.Migrations)
	}
	if fixture.application(t, "website")["release"] != "A" {
		t.Fatal("application without migrations did not deploy")
	}
}

func TestInterruptedMigrationRequiresStoppedWorkersBeforeRecoveryReleasesOwnership(t *testing.T) {
	// Arrange.
	fixture := prepareLocalDeployment(t, true)
	fixture.phase(t, "run", "release-a", fixture.releaseArguments("A")...)
	fixture.phase(t, "start", "release-b", fixture.releaseArguments("B")...)
	fixture.phase(t, "provision", "release-b")
	t.Setenv("MIGRATION_BLOCK_APP", "api")
	command := exec.CommandContext(t.Context(), fixture.binary, fixture.arguments("migrate", "release-b", "--group", "api")...)
	command.Dir = fixture.root
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+fixture.root)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill() })
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(fixture.state, "migration-blocked")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration did not reach interruption point")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Act: hard-stop the CLI before it can persist its migration outcome.
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.state, "migration-continue"), []byte("stop"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("interrupted CLI reported success")
	}
	blocked := fixture.expectFailure(t, "recover", "release-b")
	fixture.phase(t, "recover", "release-b", "--workers-stopped")
	fixture.phase(t, "start", "release-c", fixture.releaseArguments("C")...)
	fixture.phase(t, "unlock", "release-c")

	// Assert.
	if !strings.Contains(blocked, "--workers-stopped") {
		t.Fatalf("recovery did not identify interrupted migration worker: %s", blocked)
	}
	if fixture.application(t, "api")["release"] != "A" {
		t.Fatal("interrupted migration changed the serving release")
	}
}
