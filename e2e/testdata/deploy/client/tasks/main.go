package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Private helpers ------------------------------------------------------------

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("expected serve, migrate, health, rollback, or cleanup"))
	}
	if os.Args[1] == "serve" {
		fail(serve())
		return
	}
	if len(os.Args) < 3 {
		fail(fmt.Errorf("application task requires the environment argument"))
	}
	cwd, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	app, environment := filepath.Base(cwd), os.Args[2]
	if selected := os.Getenv("PM_ENV"); selected != "" && selected != environment {
		fail(fmt.Errorf("wrong environment argument"))
	}
	switch os.Args[1] {
	case "migrate":
		err = migrate(app, environment)
	case "health":
		err = health(app)
	case "rollback":
		err = rollback(app, environment)
	case "cleanup":
		_, err = run("pm", append([]string{"infra", "destroy"}, scope(environment)...)...)
	default:
		err = fmt.Errorf("unknown application task %s", os.Args[1])
	}
	fail(err)
}

func migrate(app, environment string) error {
	connection, err := query(environment, "--output", app+"DatabaseUrl", "--show-secrets")
	if err != nil {
		return err
	}
	if _, err := run("psql", connection, "-qAt", "-v", "ON_ERROR_STOP=1", "-c", "SELECT 1"); err != nil {
		return fmt.Errorf("database was not provisioned before migration: %w", err)
	}
	if err := migrationBarrier(app); err != nil {
		return err
	}
	if path := os.Getenv("MIGRATION_ORDER"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := fmt.Fprintln(file, app)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if os.Getenv("FAIL_MIGRATION_APP") == app && os.Getenv("PM_RELEASE") == "B" {
		return fmt.Errorf("injected migration failure for %s", app)
	}
	sql := "BEGIN; CREATE TABLE IF NOT EXISTS schema_versions(version text PRIMARY KEY); CREATE TABLE IF NOT EXISTS messages(id int PRIMARY KEY, message text); INSERT INTO messages VALUES(1,'preserved-row') ON CONFLICT DO NOTHING;"
	if os.Getenv("PM_RELEASE") == "B" {
		sql += "ALTER TABLE messages ADD COLUMN IF NOT EXISTS detail text;"
	}
	release := strings.ReplaceAll(os.Getenv("PM_RELEASE"), "'", "''")
	sql += "INSERT INTO schema_versions VALUES('" + release + "') ON CONFLICT DO NOTHING; COMMIT;"
	if _, err := run("psql", connection, "-qAt", "-v", "ON_ERROR_STOP=1", "-c", sql); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("APP_STATE"), app+"-database-url"), []byte(connection), 0o600); err != nil {
		return err
	}
	if os.Getenv("MIGRATION_BLOCK_APP") == app {
		state := os.Getenv("APP_STATE")
		if err := os.WriteFile(filepath.Join(state, "migration-blocked"), []byte(app), 0600); err != nil {
			return err
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(state, "migration-continue")); err == nil {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return fmt.Errorf("interrupted migration test did not stop its worker")
	}
	return nil
}

func migrationBarrier(app string) error {
	directory := os.Getenv("MIGRATION_BARRIER")
	if directory == "" {
		return nil
	}
	if err := os.WriteFile(filepath.Join(directory, app+".ready"), []byte("ready"), 0o600); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ready := true
		for _, peer := range []string{"api", "worker"} {
			if _, err := os.Stat(filepath.Join(directory, peer+".ready")); err != nil {
				ready = false
			}
		}
		if ready {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("independent migration groups did not overlap")
}

func health(app string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(os.Getenv("APP_URL") + "/" + app)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var body map[string]string
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&body) != nil || body["release"] != os.Getenv("PM_RELEASE") {
		return fmt.Errorf("application %s is not serving release %s", app, os.Getenv("PM_RELEASE"))
	}
	if app != "website" && body["message"] != "preserved-row" {
		return fmt.Errorf("application cannot read its migrated database")
	}
	if os.Getenv("FAIL_HEALTH_APP") == app && os.Getenv("PM_RELEASE") == "B" {
		if err := os.WriteFile(filepath.Join(os.Getenv("APP_STATE"), app+"-observed-B"), []byte("B"), 0o600); err != nil {
			return err
		}
		return fmt.Errorf("injected health failure after serving release B")
	}
	return nil
}

func rollback(app, environment string) error {
	urn, err := query(environment, "--resource", app+"-deployment", "--property", "urn")
	if err != nil {
		return err
	}
	if os.Getenv("PM_PREVIOUS_RELEASE") == "" || os.Getenv("PM_PREVIOUS_ARTIFACTS") == "" {
		return fmt.Errorf("rollback requires remembered artifacts")
	}
	if err := os.Setenv("PM_RELEASE", os.Getenv("PM_PREVIOUS_RELEASE")); err != nil {
		return err
	}
	if err := os.Setenv("PM_ARTIFACTS", os.Getenv("PM_PREVIOUS_ARTIFACTS")); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	command := exec.Command("mise", "exec", "--", "pulumi", "up", "--stack", os.Getenv("PM_STACK"), "--target", urn, "--yes", "--non-interactive")
	command.Dir = filepath.Clean(filepath.Join(cwd, "../../infra"))
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func query(environment string, selection ...string) (string, error) {
	args := append([]string{"infra", "query"}, scope(environment)...)
	return run("pm", append(args, selection...)...)
}

func scope(environment string) []string {
	args := []string{"--env", environment}
	if environment == "preview" {
		args = append(args, "--branch", os.Getenv("PM_BRANCH"))
	}
	return args
}

func run(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func serve() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	fmt.Println("http://" + listener.Addr().String())
	return http.Serve(listener, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		app := strings.TrimPrefix(request.URL.Path, "/")
		if app != "api" && app != "worker" && app != "website" {
			http.NotFound(response, request)
			return
		}
		release, err := os.ReadFile(filepath.Join(os.Getenv("APP_STATE"), app+"-release"))
		if err != nil {
			http.Error(response, "not deployed", http.StatusServiceUnavailable)
			return
		}
		body := map[string]string{"release": strings.TrimSpace(string(release))}
		if app != "website" {
			connection, err := os.ReadFile(filepath.Join(os.Getenv("APP_STATE"), app+"-database-url"))
			if err != nil {
				http.Error(response, "database missing", http.StatusServiceUnavailable)
				return
			}
			message, err := run("psql", string(connection), "-qAt", "-c", "SELECT message FROM messages WHERE id=1")
			if err != nil {
				http.Error(response, "database unavailable", http.StatusServiceUnavailable)
				return
			}
			body["message"] = message
		}
		response.Header().Set("Content-Type", "application/json")
		json.NewEncoder(response).Encode(body)
	}))
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
