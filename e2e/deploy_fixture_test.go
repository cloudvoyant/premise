package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudvoyant/premise/core"
)

// Private helpers ------------------------------------------------------------

type localDeploymentFixture struct {
	root      string
	binary    string
	branch    string
	state     string
	url       string
	artifacts map[string]map[string]string
}

func prepareLocalDeployment(t *testing.T, database bool) localDeploymentFixture {
	t.Helper()
	if os.Getenv("PREMISE_TEST_POSTGRES") != "1" {
		t.Skip("set PREMISE_TEST_POSTGRES=1 for real Pulumi, PostgreSQL, and application recovery tests")
	}
	fixture := localDeploymentFixture{root: filepath.Join(t.TempDir(), "client"), binary: premiseBinary(t), branch: "feature/deployment", state: t.TempDir(), artifacts: map[string]map[string]string{}}
	t.Setenv("PULUMI_HOME", t.TempDir())
	t.Setenv("APP_STATE", fixture.state)
	t.Setenv("GOWORK", "off")
	t.Setenv("PATH", filepath.Dir(fixture.binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	port := 1
	if database {
		port = startTestPostgres(t)
	}
	copyInfraFixture(t, filepath.Join(repositoryRoot(t), "e2e/testdata/deploy/client"), fixture.root)
	manifest, err := core.LoadManifest(filepath.Join(fixture.root, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Workspace.Infra.Backend = "local"
	manifest.Workspace.Infra.Backends = map[string]core.InfraBackend{"local": {URL: "file://" + t.TempDir(), AllowLocal: true}}
	manifest.Workspace.Infra.Environments = map[string]core.InfraEnvironment{"preview": {Config: map[string]string{
		"postgresql:host": "127.0.0.1", "postgresql:port": strconv.Itoa(port), "postgresql:username": "postgres", "postgresql:sslmode": "disable", "postgresql:database": "postgres",
	}}}
	manifest.Workspace.Deployment.Records = "file://" + t.TempDir()
	if !database {
		manifest.Workspace.Infra.Prerequisites = nil
		manifest.Workspace.Deployment.Applications = manifest.Workspace.Deployment.Applications[2:]
	}
	if err := core.SaveManifest(filepath.Join(fixture.root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	tasks := filepath.Join(fixture.root, "bin/tasks")
	infraCommand(t, fixture.root, "go", "build", "-o", tasks, "./tasks/main.go")
	command := exec.CommandContext(t.Context(), tasks, "serve")
	command.Dir = fixture.root
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() {
		t.Fatal("fixture application did not start")
	}
	fixture.url = scanner.Text()
	t.Setenv("APP_URL", fixture.url)
	infraCommand(t, fixture.root, "mise", "run", "infra:install")
	for _, release := range []string{"A", "B", "C"} {
		fixture.artifacts[release] = map[string]string{}
		for _, app := range manifest.Workspace.Deployment.Applications {
			path := filepath.Join(t.TempDir(), app.Project+"-"+release+".txt")
			if err := os.WriteFile(path, []byte(release+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			fixture.artifacts[release][app.Project] = path
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cleanup := exec.CommandContext(ctx, fixture.binary, "infra", "destroy", "--env", "preview", "--branch", fixture.branch)
		cleanup.Dir = fixture.root
		cleanup.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+fixture.root)
		if output, err := cleanup.CombinedOutput(); err != nil {
			t.Errorf("fixture cleanup: %v\n%s", err, output)
		}
	})
	return fixture
}

func startTestPostgres(t *testing.T) int {
	t.Helper()
	if bindir, err := exec.Command("pg_config", "--bindir").Output(); err == nil {
		t.Setenv("PATH", strings.TrimSpace(string(bindir))+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for _, tool := range []string{"initdb", "pg_ctl", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("PostgreSQL tool %s is required: %v", tool, err)
		}
	}
	directory := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	infraCommand(t, directory, "initdb", "-D", filepath.Join(directory, "data"), "-U", "postgres", "-A", "trust", "--no-locale")
	// PostgreSQL sockets have a short OS path limit; tests use TCP only.
	options := fmt.Sprintf("-h 127.0.0.1 -p %d -k ''", port)
	infraCommand(t, directory, "pg_ctl", "-D", filepath.Join(directory, "data"), "-l", filepath.Join(directory, "postgres.log"), "-o", options, "-w", "start")
	t.Cleanup(func() {
		command := exec.Command("pg_ctl", "-D", filepath.Join(directory, "data"), "-m", "immediate", "-w", "stop")
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("stop fixture PostgreSQL: %v\n%s", err, output)
		}
	})
	return port
}

func (fixture localDeploymentFixture) arguments(phase, id string, args ...string) []string {
	base := []string{"deploy", phase, "--env", "preview", "--branch", fixture.branch, "--run-id", id}
	return append(base, args...)
}

func (fixture localDeploymentFixture) releaseArguments(release string) []string {
	args := []string{"--release", release}
	for _, app := range []string{"api", "worker", "website"} {
		if reference := fixture.artifacts[release][app]; reference != "" {
			args = append(args, "--artifact", app+"="+reference)
		}
	}
	return args
}

func (fixture localDeploymentFixture) phase(t *testing.T, phase, id string, args ...string) string {
	t.Helper()
	return infraCommand(t, fixture.root, fixture.binary, fixture.arguments(phase, id, args...)...)
}

func (fixture localDeploymentFixture) expectFailure(t *testing.T, phase, id string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), fixture.binary, fixture.arguments(phase, id, args...)...)
	command.Dir = fixture.root
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+fixture.root)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("deployment %s unexpectedly succeeded:\n%s", phase, output)
	}
	return string(output)
}

func (fixture localDeploymentFixture) expectInfraFailure(t *testing.T, operation string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), fixture.binary, "infra", operation, "--env", "preview", "--branch", fixture.branch)
	command.Dir = fixture.root
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+fixture.root)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("standalone infra %s bypassed deployment ownership", operation)
	}
	return string(output)
}

func (fixture localDeploymentFixture) freshCheckout(t *testing.T) localDeploymentFixture {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "client")
	copyInfraFixture(t, fixture.root, destination)
	fixture.root = destination
	return fixture
}

func (fixture localDeploymentFixture) plan(t *testing.T) core.DeploymentPlan {
	t.Helper()
	output := infraCommand(t, fixture.root, fixture.binary, "deploy", "plan", "--env", "preview", "--branch", fixture.branch, "--json")
	var plan core.DeploymentPlan
	if err := json.Unmarshal([]byte(output), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func (fixture localDeploymentFixture) application(t *testing.T, app string) map[string]string {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(fixture.url + "/" + app)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]string
	if response.StatusCode != http.StatusOK {
		t.Fatalf("application %s returned HTTP %d", app, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}
