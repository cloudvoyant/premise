package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Public API -----------------------------------------------------------------

// TestPulumiLive deploys the embedded client fixture with the published tandem
// templates. It verifies actual migrations, app bindings, recovery, and cleanup.
func TestPulumiLive(t *testing.T) {
	if os.Getenv("PREMISE_LIVE_E2E") != "1" {
		t.Skip("set PREMISE_LIVE_E2E=1 in the protected live-infra job")
	}
	for _, key := range []string{"PREMISE_BIN", "PREMISE_PULUMI_REGISTRY", "PREMISE_RC_VERSION", "PREMISE_ACTION_PATH"} {
		if os.Getenv(key) == "" {
			t.Fatalf("%s is required for published-RC live validation", key)
		}
	}
	fixture := filepath.Join(repositoryRoot(t), "e2e/testdata/pulumi/neon-vercel")
	for _, step := range [][]string{{"npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund"}, {"node", "live.mjs"}} {
		command := exec.Command(step[0], step[1:]...)
		command.Dir = fixture
		// Provider output and process errors can contain secrets. The harness writes
		// only non-secret assertions to rollback.json; never echo captured diagnostics.
		if err := command.Run(); err != nil {
			t.Fatalf("%s failed; inspect non-secret rollback evidence", step[0])
		}
	}
}
