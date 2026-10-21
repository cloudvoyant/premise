package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cloudvoyant/premise/core/providers"
)

// ReleaseRecord remembers the artifacts that passed deployment and health checks.
// Premise saves it as last-success.json so another runner can restore that release.
// It contains recovery references, never database credentials or application secrets.
type ReleaseRecord struct {
	RunID     string            `json:"run_id,omitempty"`
	Release   string            `json:"release"`
	Artifacts map[string]string `json:"artifacts"`
	Program   string            `json:"program"`
	Stack     string            `json:"stack"`
	Backend   string            `json:"backend"`
	Completed time.Time         `json:"completed"`
	Outputs   map[string]string `json:"outputs,omitempty"`
}

// Private helpers ------------------------------------------------------------

var recordNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type deploymentRun struct {
	ID       string           `json:"id"`
	Options  DeployOptions    `json:"options"`
	Project  string           `json:"project"`
	Stack    string           `json:"stack"`
	Backend  string           `json:"backend"`
	Phase    string           `json:"phase"`
	Previous *ReleaseRecord   `json:"previous,omitempty"`
	Groups   []MigrationGroup `json:"migrations"`
}

type deploymentLock struct {
	RunID   string    `json:"run_id"`
	Created time.Time `json:"created"`
}

type migrationOutcome struct {
	Status string `json:"status"`
}

func deploymentRecordStore(root string, manifest Config, destination InfraResolution) (providers.ObjectStore, error) {
	base := manifest.Workspace.Deployment.Records
	if base == "" {
		base = strings.TrimRight(destination.BackendURL, "/") + "/.premise-releases"
	}
	if !strings.Contains(base, "://") {
		directory, err := deploymentRecordDirectory(root, manifest, destination)
		if err != nil {
			return providers.ObjectStore{}, err
		}
		return providers.ObjectStore{URL: (&url.URL{Scheme: "file", Path: filepath.ToSlash(directory)}).String()}, nil
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return providers.ObjectStore{}, fmt.Errorf("deployment.records must be a credential-free storage prefix")
	}
	if parsed.Scheme != "s3" && parsed.Scheme != "gs" && parsed.Scheme != "file" {
		return providers.ObjectStore{}, fmt.Errorf("deployment.records supports s3://, gs://, and explicit local file:// storage")
	}
	if parsed.Scheme == "file" {
		if parsed.Host != "" || !filepath.IsAbs(parsed.Path) {
			return providers.ObjectStore{}, fmt.Errorf("local deployment records require an absolute file URL")
		}
		if runningInCI() && !strings.HasPrefix(destination.BackendURL, "file://") {
			return providers.ObjectStore{}, fmt.Errorf("CI deployment records require shared s3:// or gs:// storage, not runner-local files")
		}
	} else if parsed.Host == "" {
		return providers.ObjectStore{}, fmt.Errorf("deployment record prefix requires a bucket")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + destination.ProjectName + "/" + destination.Stack
	if parsed.Scheme == "file" {
		if err := validateDeploymentRecordDirectory(root, parsed.Path, manifest); err != nil {
			return providers.ObjectStore{}, err
		}
	}
	return providers.ObjectStore{URL: parsed.String()}, nil
}

// acquireInfrastructureOwnership prevents standalone commands from mutating a
// stack during an environment CI flow or application deployment.
func acquireInfrastructureOwnership(ctx context.Context, root string, destination InfraResolution) (func() error, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	if manifest.Workspace.Deployment == nil {
		return func() error { return nil }, nil
	}
	store, err := deploymentRecordStore(root, manifest, destination)
	if err != nil {
		return nil, err
	}
	// A client recovery task may call infra commands within its existing run.
	if id := os.Getenv("PM_RUN_ID"); id != "" {
		if _, err := assertDeploymentOwner(ctx, store, id); err != nil {
			return nil, err
		}
		return func() error { return nil }, nil
	}
	id := fmt.Sprintf("infra-%d", time.Now().UnixNano())
	if err := writeDeploymentRecord(ctx, store, "lock.json", deploymentLock{RunID: id, Created: time.Now().UTC()}, "*"); err != nil {
		return nil, fmt.Errorf("cannot mutate infrastructure while another run owns the environment lock: %w", err)
	}
	return func() error {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		version, err := assertDeploymentOwner(cleanup, store, id)
		if err != nil {
			return err
		}
		return store.Delete(cleanup, "lock.json", version)
	}, nil
}

func writeDeploymentRecord(ctx context.Context, store providers.ObjectStore, key string, value any, version string) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return store.Put(ctx, key, append(data, '\n'), version)
}

func readDeploymentRun(ctx context.Context, store providers.ObjectStore, id string) (deploymentRun, string, error) {
	var run deploymentRun
	object, err := store.Read(ctx, "runs/"+id+".json")
	if err != nil {
		return run, "", fmt.Errorf("read deployment run %s: %w", id, err)
	}
	if err := json.Unmarshal(object.Data, &run); err != nil || run.ID != id {
		return run, "", fmt.Errorf("deployment run %s contains invalid metadata", id)
	}
	return run, object.Version, nil
}

func assertDeploymentOwner(ctx context.Context, store providers.ObjectStore, id string) (string, error) {
	object, err := store.Read(ctx, "lock.json")
	if err != nil {
		return "", fmt.Errorf("deployment %s does not own the environment lock: %w", id, err)
	}
	var lock deploymentLock
	if err := json.Unmarshal(object.Data, &lock); err != nil || lock.RunID != id {
		return "", fmt.Errorf("environment is locked by another deployment; run %s cannot proceed", id)
	}
	return object.Version, nil
}

func readSuccessfulRelease(ctx context.Context, store providers.ObjectStore) (*ReleaseRecord, error) {
	object, err := store.Read(ctx, "last-success.json")
	if errors.Is(err, providers.ErrObjectNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var release ReleaseRecord
	if err := json.Unmarshal(object.Data, &release); err != nil {
		return nil, fmt.Errorf("decode last successful deployment: %w", err)
	}
	return &release, nil
}

func deploymentRecordDirectory(root string, manifest Config, destination InfraResolution) (string, error) {
	name := destination.ProjectName
	if name == "." || name == ".." || !recordNamespacePattern.MatchString(name) {
		return "", fmt.Errorf("infrastructure program name must be a safe record namespace")
	}
	directory := manifest.Workspace.Deployment.Records
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, directory)
	}
	directory, err := filepath.Abs(filepath.Join(directory, name, destination.Stack))
	if err != nil {
		return "", err
	}
	if err := validateDeploymentRecordDirectory(root, directory, manifest); err != nil {
		return "", err
	}
	return directory, nil
}

func validateDeploymentRecordDirectory(root, path string, manifest Config) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	recordAbs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for _, project := range manifest.Workspace.Projects {
		projectPath := filepath.Join(rootAbs, project.Path)
		rel, err := filepath.Rel(projectPath, recordAbs)
		if err != nil {
			return err
		}
		if rel == "." || !escapesRoot(rel) {
			return fmt.Errorf("deployment records cannot be inside project %s", project.Name)
		}
	}
	rel, err := filepath.Rel(rootAbs, recordAbs)
	if err != nil {
		return err
	}
	if !escapesRoot(rel) {
		return rejectSymlinkComponents(rootAbs, recordAbs)
	}
	info, err := os.Lstat(recordAbs)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("deployment record path is a symlink")
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readReleaseOutputs(ctx context.Context, runner infraCommandRunner, d InfraResolution, names []string, env map[string]string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	scopedEnv := map[string]string{}
	for k, v := range env {
		scopedEnv[k] = v
	}
	scopedEnv["PULUMI_BACKEND_URL"] = d.BackendURL
	env = scopedEnv
	var output bytes.Buffer
	private := infraCommandRunner{stdout: &output, stderr: io.Discard, private: true}
	if err := private.run(ctx, d.Directory, env, "pulumi", "stack", "output", "--stack", d.Stack, "--json", "--non-interactive"); err != nil {
		return nil, fmt.Errorf("read release identities failed")
	}
	values := map[string]any{}
	if err := json.Unmarshal(output.Bytes(), &values); err != nil {
		return nil, fmt.Errorf("invalid release output JSON")
	}
	result := map[string]string{}
	for _, name := range names {
		value, ok := values[name].(string)
		if !ok || value == "" || value == "[secret]" {
			return nil, fmt.Errorf("release output %s must be a non-secret string", name)
		}
		result[name] = value
	}
	return result, nil
}
