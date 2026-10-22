package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// GCP authenticates Google Cloud tools through local login or CI federation.
type GCP struct{}

// Public API -----------------------------------------------------------------

// EnsureEnvironment checks the credential file used by CI and explicit local credentials.
func (GCP) EnsureEnvironment(ci bool) error {
	if !ci && os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		return nil // Local ADC has a standard SDK search path.
	}
	if err := requireVariables("gcp", "GOOGLE_APPLICATION_CREDENTIALS"); err != nil {
		return err
	}
	if info, err := os.Stat(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("gcp: GOOGLE_APPLICATION_CREDENTIALS must name a readable credential file; run pm infra auth --ci (see docs/providers/gcp.md)")
	}
	return nil
}

// Authenticate prepares local OAuth credentials or a CI workload identity credential file.
func (p GCP) Authenticate(ctx context.Context, root string, ci bool, stdin io.Reader, stdout, stderr io.Writer) (map[string]string, error) {
	if !ci {
		if err := runCommand(ctx, root, stdin, stdout, stderr, "gcloud", "auth", "application-default", "login"); err != nil {
			return nil, err
		}
		return nil, runCommand(ctx, root, stdin, stdout, stderr, "gcloud", "auth", "login")
	}
	if path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
		if err := p.EnsureEnvironment(true); err != nil {
			return nil, err
		}
		return map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": path, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE": path}, nil
	}
	if err := requireVariables("gcp", "GCP_WORKLOAD_IDENTITY_PROVIDER", "GCP_SERVICE_ACCOUNT"); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "premise-gcp-auth-*")
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(directory)
		}
	}()
	token := os.Getenv("CI_OIDC_TOKEN_FILE")
	if token == "" {
		token = filepath.Join(directory, "identity-token")
		audience := "//iam.googleapis.com/" + os.Getenv("GCP_WORKLOAD_IDENTITY_PROVIDER")
		if err := CIIdentityToken(ctx, audience, token); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(directory, "credentials.json")
	if err := runCommand(ctx, root, stdin, stdout, stderr, "gcloud", "iam", "workload-identity-pools", "create-cred-config", os.Getenv("GCP_WORKLOAD_IDENTITY_PROVIDER"), "--service-account="+os.Getenv("GCP_SERVICE_ACCOUNT"), "--credential-source-file="+token, "--output-file="+path); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	success = true
	return map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": path, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE": path}, nil
}

// Private helpers ------------------------------------------------------------

func readGCSObject(ctx context.Context, location *url.URL) (StoredObject, error) {
	metadata, err := objectCommand(ctx, "gcloud", "storage", "objects", "describe", location.String(), "--format=json")
	if objectNotFound(err) {
		return StoredObject{}, ErrObjectNotFound
	}
	if err != nil {
		return StoredObject{}, fmt.Errorf("read %s: %w", location, err)
	}
	var version struct {
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(metadata, &version); err != nil || version.Generation == "" {
		return StoredObject{}, fmt.Errorf("GCS did not return a deployment record generation")
	}
	data, err := objectCommand(ctx, "gcloud", "storage", "cat", location.String()+"#"+version.Generation)
	return StoredObject{Data: data, Version: version.Generation}, err
}

func writeGCSObject(ctx context.Context, location *url.URL, data []byte, version string) error {
	path, cleanup, err := temporaryObjectBody(data)
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{"storage", "cp", path, location.String(), "--quiet"}
	if version == "*" {
		args = append(args, "--if-generation-match=0")
	} else if version != "" {
		args = append(args, "--if-generation-match="+strings.TrimSpace(version))
	}
	_, err = objectCommand(ctx, "gcloud", args...)
	return err
}
