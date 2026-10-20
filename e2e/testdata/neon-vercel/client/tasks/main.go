package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Private helpers ------------------------------------------------------------

type event struct {
	Inputs struct {
		Scenario string `json:"scenario"`
		Version  string `json:"version"`
	} `json:"inputs"`
}

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("expected prepare, migrate, health, rollback, cleanup, or assert"))
	}
	operation := os.Args[1]
	if operation == "prepare" {
		fail(prepare())
		return
	}
	if len(os.Args) < 3 {
		fail(fmt.Errorf("environment argument is required"))
	}
	environment := os.Args[2]
	if environment == "preview" && os.Getenv("PM_BRANCH") == "" {
		fail(fmt.Errorf("preview application tasks require PM_BRANCH"))
	}
	if selected := os.Getenv("PM_ENV"); selected != "" && selected != environment {
		fail(fmt.Errorf("environment argument does not match PM_ENV"))
	}
	switch operation {
	case "migrate":
		fail(migrate(environment))
	case "health":
		fail(health(environment))
	case "rollback":
		fail(rollback(environment))
	case "cleanup":
		fail(cleanup(environment))
	case "assert":
		fail(assertRelease(environment))
	case "prune":
		fail(pruneDeployments())
	default:
		fail(fmt.Errorf("unknown client task %q", operation))
	}
}

func settings() (string, string, error) {
	if value := os.Getenv("GITHUB_EVENT_PATH"); value != "" {
		data, err := os.ReadFile(value)
		if err != nil {
			return "", "", err
		}
		var e event
		if err := json.Unmarshal(data, &e); err != nil {
			return "", "", err
		}
		if e.Inputs.Scenario != "" && e.Inputs.Version != "" {
			return e.Inputs.Scenario, e.Inputs.Version, nil
		}
	}
	return "", "", fmt.Errorf("cloud validation requires workflow inputs scenario and version")
}

func prepare() error {
	scenario, version, err := settings()
	if err != nil {
		return err
	}
	switch scenario {
	case "parallel", "shared", "none", "fail-migration", "fail-apply", "rollback":
	default:
		return fmt.Errorf("unknown validation scenario %q", scenario)
	}
	if strings.ContainsAny(version, "/\\\r\n") || !strings.HasPrefix(version, "v") {
		return fmt.Errorf("expected a released version tag")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	source, err := os.ReadFile(filepath.Join(root, "premise.base.yaml"))
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := yaml.Unmarshal(source, &manifest); err != nil {
		return err
	}
	workspace := manifest["workspace"].(map[string]any)
	infra := workspace["infra"].(map[string]any)
	environments := infra["environments"].(map[string]any)
	preview := environments["preview"].(map[string]any)
	preview["config"].(map[string]any)["client:scenario"] = scenario
	if scenario == "shared" {
		workspace["deployment"].(map[string]any)["migration_groups"] = map[string][]string{"shared-history": {"api", "worker"}}
		var prerequisites []string
		for _, value := range infra["prerequisites"].([]any) {
			resource := value.(string)
			if !strings.Contains(resource, "::worker-") {
				prerequisites = append(prerequisites, resource)
			}
		}
		infra["prerequisites"] = prerequisites
	}
	if scenario == "none" {
		infra["prerequisites"] = []string{}
	}
	for _, app := range []string{"api", "worker", "web"} {
		directory := filepath.Join(root, "apps", app)
		if err := os.MkdirAll(directory, 0755); err != nil {
			return err
		}
		tasks := "[tasks.health]\nrun = '../../bin/cloud-tasks health'\n[tasks.rollback]\nrun = '../../bin/cloud-tasks rollback'\n[tasks.cleanup]\nrun = '../../bin/cloud-tasks cleanup'\n"
		if app != "web" && scenario != "none" {
			tasks += "[tasks.migrate]\nrun = '../../bin/cloud-tasks migrate'\n"
		}
		if err := os.WriteFile(filepath.Join(directory, "mise.toml"), []byte(tasks), 0644); err != nil {
			return err
		}
	}
	configuration, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "premise.yaml"), configuration, 0644); err != nil {
		return err
	}
	for _, release := range []string{"A", "B"} {
		target := filepath.Join(root, ".releases", version+"-"+release, "web")
		if err := copySource(filepath.Join(root, "apps/web"), target); err != nil {
			return err
		}
	}
	return nil
}

func copySource(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == ".output" || entry.Name() == ".vercel" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".env") {
			return fmt.Errorf("source snapshot contains an environment file")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("source snapshot contains a non-regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}

func scope(environment string) []string {
	result := []string{"--env", environment}
	if environment == "preview" {
		result = append(result, "--branch", os.Getenv("PM_BRANCH"))
	}
	return result
}

func query(environment string, selection ...string) (string, error) {
	args := append([]string{"infra", "query"}, scope(environment)...)
	return run("pm", append(args, selection...)...)
}

func sql(connection, statement string) (string, error) {
	command := exec.Command("psql", "-qAt", "-v", "ON_ERROR_STOP=1", "-c", statement)
	command.Env = append(os.Environ(), "PGDATABASE="+connection)
	// libpq credentials remain in the process environment; diagnostics can include them.
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("application SQL operation failed")
	}
	return strings.TrimSpace(string(output)), nil
}

func migrate(environment string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	app := filepath.Base(cwd)
	scenario, _, err := settings()
	if err != nil {
		return err
	}
	connection, err := query(environment, "--output", app+"DatabaseUrl", "--show-secrets")
	if err != nil {
		return err
	}
	if _, err := sql(connection, "SELECT 1"); err != nil {
		return fmt.Errorf("database was not provisioned before migration: %w", err)
	}
	release := os.Getenv("PM_RELEASE")
	if scenario != "shared" {
		if err := barrier(app); err != nil {
			return err
		}
	}
	if scenario == "fail-migration" && app == "api" && strings.HasSuffix(release, "-B") {
		return fmt.Errorf("injected migration failure")
	}
	version := strings.ReplaceAll(app+":"+release, "'", "''")
	statement := "BEGIN; CREATE TABLE IF NOT EXISTS schema_versions(version text PRIMARY KEY); CREATE TABLE IF NOT EXISTS messages(id int PRIMARY KEY,message text); INSERT INTO messages VALUES(1,'preserved-row') ON CONFLICT DO NOTHING;"
	if scenario == "shared" && app == "worker" {
		prior := strings.ReplaceAll("api:"+release, "'", "''")
		count, err := sql(connection, "SELECT count(*) FROM schema_versions WHERE version='"+prior+"'")
		if err != nil || count != "1" {
			return fmt.Errorf("shared migration history ran out of order")
		}
	}
	if strings.HasSuffix(release, "-B") {
		statement += "ALTER TABLE messages ADD COLUMN IF NOT EXISTS detail text;"
	}
	statement += "INSERT INTO schema_versions VALUES('" + version + "') ON CONFLICT DO NOTHING; COMMIT;"
	_, err = sql(connection, statement)
	return err
}

func barrier(app string) error {
	base := strings.TrimRight(os.Getenv("PULUMI_BACKEND_URL"), "/") + "/.premise-validation/" + os.Getenv("PM_STACK") + "/" + os.Getenv("PM_RUN_ID")
	if !strings.HasPrefix(base, "gs://") {
		return fmt.Errorf("cloud migration barrier requires its dedicated GCS validation prefix")
	}
	file, err := os.CreateTemp("", "premise-worker-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	file.WriteString(app)
	file.Close()
	if _, err := run("gcloud", "storage", "cp", file.Name(), base+"/"+app, "--quiet"); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		ready := true
		for _, peer := range []string{"api", "worker"} {
			if exec.Command("gcloud", "storage", "objects", "describe", base+"/"+peer, "--format=value(name)").Run() != nil {
				ready = false
			}
		}
		if ready {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("independent migration jobs did not overlap")
}

func response(environment string) (map[string]string, error) {
	address, err := query(environment, "--output", "applicationUrl")
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, address+"/api/health", nil)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("VERCEL_AUTOMATION_BYPASS_SECRET"); token != "" {
		request.Header.Set("x-vercel-protection-bypass", token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	result, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("application request failed")
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("application returned HTTP %d", result.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(io.LimitReader(result.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

func health(environment string) error {
	body, err := response(environment)
	if err != nil {
		return err
	}
	if body["release"] != os.Getenv("PM_RELEASE") {
		return fmt.Errorf("application serves %s instead of the requested release", body["release"])
	}
	scenario, _, err := settings()
	if err != nil {
		return err
	}
	expected := "preserved-row"
	if scenario == "none" {
		expected = "unmigrated"
	}
	if body["api"] != expected || body["worker"] != expected {
		return fmt.Errorf("application cannot read the expected database data")
	}
	if scenario == "rollback" && strings.HasSuffix(os.Getenv("PM_RELEASE"), "-B") {
		fmt.Println("Observed real release B with preserved data before injected health failure")
		return fmt.Errorf("injected health failure after serving release B")
	}
	return nil
}

func rollback(environment string) error {
	if os.Getenv("PM_PREVIOUS_RELEASE") == "" || os.Getenv("PM_PREVIOUS_ARTIFACTS") == "" {
		return fmt.Errorf("rollback requires retained artifact references")
	}
	var targets []string
	for _, name := range []string{"web-deployment", "web-alias"} {
		urn, err := query(environment, "--resource", name, "--property", "urn")
		if err != nil {
			return err
		}
		targets = append(targets, "--target", urn)
	}
	root, err := workspaceRoot()
	if err != nil {
		return err
	}
	args := append([]string{"exec", "--", "pulumi", "up", "--stack", os.Getenv("PM_STACK"), "--yes", "--non-interactive"}, targets...)
	command := exec.Command("mise", args...)
	command.Dir = filepath.Join(root, "infra")
	command.Env = append(os.Environ(), "PM_RELEASE="+os.Getenv("PM_PREVIOUS_RELEASE"), "PM_ARTIFACTS="+os.Getenv("PM_PREVIOUS_ARTIFACTS"))
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func cleanup(environment string) error {
	_, err := run("pm", append([]string{"infra", "destroy"}, scope(environment)...)...)
	return err
}

func assertRelease(environment string) error {
	body, err := response(environment)
	if err != nil {
		return err
	}
	expected := os.Getenv("EXPECTED_RELEASE")
	expectedData := "preserved-row"
	if scenario, _, err := settings(); err == nil && scenario == "none" {
		expectedData = "unmigrated"
	}
	if body["api"] != expectedData || body["worker"] != expectedData {
		return fmt.Errorf("application data did not survive deployment or recovery")
	}
	if body["release"] != expected {
		return fmt.Errorf("serving release %s, expected %s", body["release"], expected)
	}
	scenario, _, err := settings()
	if err != nil {
		return err
	}
	if scenario != "none" {
		connection, err := query(environment, "--output", "apiDatabaseUrl", "--show-secrets")
		if err != nil {
			return err
		}
		versions, err := sql(connection, "SELECT string_agg(version, ',' ORDER BY version) FROM schema_versions")
		if err != nil {
			return err
		}
		if !strings.Contains(versions, "api:"+strings.TrimSuffix(expected, "-B")+"-A") && !strings.Contains(versions, "api:"+expected) {
			return fmt.Errorf("expected baseline migration history is absent")
		}
		if scenario != "fail-migration" && strings.HasSuffix(expected, "-B") && !strings.Contains(versions, "api:"+expected) {
			return fmt.Errorf("expected updated migration history is absent")
		}
		fmt.Println("Verified migration history:", versions)
		if scenario == "rollback" || scenario == "fail-apply" {
			columns, err := sql(connection, "SELECT count(*) FROM information_schema.columns WHERE table_name='messages' AND column_name='detail'")
			if err != nil || columns != "1" {
				return fmt.Errorf("recovery removed the compatible B schema")
			}
		}
	}
	fmt.Println("Verified serving release and database data:", expected, body["api"], body["worker"])
	planData, err := run("pm", append([]string{"deploy", "plan", "--json"}, scope(environment)...)...)
	if err != nil {
		return err
	}
	var plan struct {
		RecordsURL string            `json:"records_url"`
		Migrations []json.RawMessage `json:"migrations"`
	}
	if err := json.Unmarshal([]byte(planData), &plan); err != nil {
		return err
	}
	wantGroups := 2
	if scenario == "shared" {
		wantGroups = 1
	}
	if scenario == "none" {
		wantGroups = 0
	}
	if len(plan.Migrations) != wantGroups {
		return fmt.Errorf("migration matrix has %d groups, expected %d", len(plan.Migrations), wantGroups)
	}
	successData, err := run("gcloud", "storage", "cat", plan.RecordsURL+"/last-success.json")
	if err != nil {
		return err
	}
	var success struct {
		Release   string            `json:"release"`
		Artifacts map[string]string `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(successData), &success); err != nil {
		return err
	}
	if success.Release != expected {
		return fmt.Errorf("last-success record changed to %s instead of %s", success.Release, expected)
	}
	_, version, err := settings()
	if err != nil {
		return err
	}
	for _, app := range []string{"api", "worker", "web"} {
		reference := ".releases/" + version + expected[len(expected)-2:] + "/web"
		if success.Artifacts[app] != reference {
			return fmt.Errorf("last-success lost the immutable %s artifact reference", app)
		}
	}
	runData, err := run("gcloud", "storage", "cat", plan.RecordsURL+"/last-run.json")
	if err != nil {
		return err
	}
	var result struct {
		Succeeded         bool `json:"succeeded"`
		RecoverySucceeded bool `json:"recovery_succeeded"`
	}
	if err := json.Unmarshal([]byte(runData), &result); err != nil {
		return err
	}
	failed := scenario == "rollback" || scenario == "fail-apply" || scenario == "fail-migration"
	if result.Succeeded == failed || (failed && !result.RecoverySucceeded) {
		return fmt.Errorf("original deployment and recovery outcomes are incorrect")
	}
	fmt.Println("Verified migration job count, original run outcome, recovery result, and retained last-success references")
	return nil
}

// pruneDeployments deletes only retained fixture deployments tagged with this stack.
func pruneDeployments() error {
	stack := os.Getenv("PM_STACK")
	if !strings.HasPrefix(stack, "preview-") {
		return fmt.Errorf("retained test cleanup requires an explicit preview stack")
	}
	team := "team_18gyVULDM7SQHEIxvCMQz2PI"
	project := "prj_MsMfmDj4vMZM1RnTMfsSw7CF1rXQ"
	parameters := url.Values{"teamId": {team}, "projectId": {project}, "limit": {"100"}}
	for {
		var page struct {
			Deployments []struct {
				UID  string            `json:"uid"`
				Meta map[string]string `json:"meta"`
			} `json:"deployments"`
			Pagination struct {
				Next *int64 `json:"next"`
			} `json:"pagination"`
		}
		if err := vercel(http.MethodGet, "/v6/deployments?"+parameters.Encode(), &page); err != nil {
			return err
		}
		for _, deployment := range page.Deployments {
			if deployment.Meta["premiseStack"] != stack {
				continue
			}
			if err := vercel(http.MethodDelete, "/v13/deployments/"+url.PathEscape(deployment.UID)+"?teamId="+url.QueryEscape(team), nil); err != nil {
				return err
			}
			fmt.Println("Removed owned retained deployment", deployment.UID)
		}
		if page.Pagination.Next == nil {
			if err := verifyDatabaseCleanup(stack); err != nil {
				return err
			}
			base := strings.TrimRight(os.Getenv("PULUMI_BACKEND_URL"), "/")
			if !strings.HasPrefix(base, "gs://") {
				return fmt.Errorf("validation cleanup requires its GCS backend")
			}
			command := exec.Command("gcloud", "storage", "rm", "--recursive", base+"/.premise-validation/"+stack+"/", "--quiet")
			output, err := command.CombinedOutput()
			if err != nil && !strings.Contains(string(output), "No URLs matched") && !strings.Contains(string(output), "matched no objects") {
				return fmt.Errorf("owned migration barrier cleanup failed")
			}
			return nil
		}
		parameters.Set("until", fmt.Sprint(*page.Pagination.Next))
	}
}

// verifyDatabaseCleanup checks the provider after Pulumi destroys the owned preview.
func verifyDatabaseCleanup(stack string) error {
	request, err := http.NewRequest(http.MethodGet, "https://console.neon.tech/api/v2/projects/bitter-river-81933503/branches", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv("NEON_API_KEY"))
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Neon cleanup verification failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Neon cleanup verification returned HTTP %d", response.StatusCode)
	}
	var data struct {
		Branches []struct {
			Name string `json:"name"`
		} `json:"branches"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&data); err != nil {
		return err
	}
	for _, branch := range data.Branches {
		if branch.Name == "premise-"+stack+"-api" || branch.Name == "premise-"+stack+"-worker" {
			return fmt.Errorf("owned Neon branch still exists after destroy")
		}
	}
	fmt.Println("Verified owned Neon branches are absent")
	return nil
}

func vercel(method, path string, destination any) error {
	token := os.Getenv("VERCEL_API_TOKEN")
	if token == "" {
		return fmt.Errorf("VERCEL_API_TOKEN is required for owned test cleanup")
	}
	request, err := http.NewRequest(method, "https://api.vercel.com"+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Vercel cleanup request failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound && method == http.MethodDelete {
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Vercel cleanup returned HTTP %d", response.StatusCode)
	}
	if destination != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(destination)
	}
	return nil
}

func workspaceRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "premise.base.yaml")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("client workspace not found")
		}
		directory = parent
	}
}

func run(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%s operation failed: %w", name, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
