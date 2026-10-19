package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InfraProvider authenticates deployment tools and checks their standard variables.
// It never resolves application secrets.
type InfraProvider interface {
	EnsureEnvironment(ci bool) error
	Authenticate(context.Context, string, bool, io.Reader, io.Writer, io.Writer) (map[string]string, error)
}

// Public API -----------------------------------------------------------------

// InfrastructureProvider returns a supported provider, independently of Pulumi resources.
func InfrastructureProvider(name string) (InfraProvider, bool) {
	switch name {
	case "gcp", "neon", "vercel":
		return infraProvider{name: name}, true
	default:
		return nil, false
	}
}

// EnsureInfrastructureEnvironment checks declared providers before infrastructure work.
func EnsureInfrastructureEnvironment(root string, ci bool) error {
	names, err := infrastructureProviderNames(root)
	if err != nil {
		return err
	}
	for _, name := range names {
		if provider, ok := InfrastructureProvider(name); ok {
			if err := provider.EnsureEnvironment(ci); err != nil {
				return err
			}
		}
	}
	return nil
}

// AuthenticateInfrastructure prepares native credentials, then the optional root fallback.
// Returned variables contain credential file paths, never token values.
func AuthenticateInfrastructure(ctx context.Context, root string, ci bool, stdin io.Reader, stdout, stderr io.Writer) (map[string]string, error) {
	names, err := infrastructureProviderNames(root)
	if err != nil {
		return nil, err
	}
	environment := map[string]string{}
	completed := false
	var createdDirectories []string
	defer func() {
		if !completed {
			for _, directory := range createdDirectories {
				os.RemoveAll(directory)
			}
		}
	}()
	var unsupported []string
	for _, name := range names {
		provider, ok := InfrastructureProvider(name)
		if !ok {
			unsupported = append(unsupported, name)
			continue
		}
		values, err := provider.Authenticate(ctx, root, ci, stdin, stdout, stderr)
		if err != nil {
			return nil, err
		}
		if ci && name == "gcp" && os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
			if path := values["GOOGLE_APPLICATION_CREDENTIALS"]; filepath.IsAbs(path) {
				createdDirectories = append(createdDirectories, filepath.Dir(path))
			}
		}
		for key, value := range values {
			environment[key] = value
		}
	}
	if ci {
		data, err := os.ReadFile(filepath.Join(root, "mise.toml"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		var fallback bool
		if err == nil {
			config, err := ExtractMiseConfig("workspace", data)
			if err != nil {
				return nil, err
			}
			_, fallback = config.Tasks["ci:auth"]
		}
		if fallback {
			additions := []string{}
			for key, value := range environment {
				additions = append(additions, key+"="+value)
			}
			runner := miseRunner{Stdin: stdin, Stdout: stdout, Stderr: stderr, Ceiling: filepath.Dir(root)}
			if err := runner.run(ctx, root, additions, "run", "ci:auth"); err != nil {
				return nil, fmt.Errorf("root ci:auth failed: %w", err)
			}
		} else if len(unsupported) > 0 {
			return nil, fmt.Errorf("unsupported infrastructure providers %s: declare ci:auth in the root mise.toml", strings.Join(unsupported, ", "))
		}
	} else if len(unsupported) > 0 {
		return nil, fmt.Errorf("authenticate unsupported providers %s using their own CLIs", strings.Join(unsupported, ", "))
	}
	completed = true
	return environment, nil
}

// Private helpers ------------------------------------------------------------

type infraProvider struct{ name string }

func infrastructureProviderNames(root string) ([]string, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return nil, err
	}
	if manifest.Workspace.Infra == nil {
		return nil, nil
	}
	return manifest.Workspace.Infra.Providers, nil
}

func (p infraProvider) EnsureEnvironment(ci bool) error {
	switch p.name {
	case "neon":
		return requireProviderVariables(p.name, "NEON_API_KEY")
	case "vercel":
		return requireProviderVariables(p.name, "VERCEL_API_TOKEN")
	case "gcp":
		if !ci && os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
			return nil
		} // Local ADC has a standard SDK search path.
		if err := requireProviderVariables(p.name, "GOOGLE_APPLICATION_CREDENTIALS"); err != nil {
			return err
		}
		if info, err := os.Stat(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("gcp: GOOGLE_APPLICATION_CREDENTIALS must name a readable credential file; run pm infra auth --ci (see docs/providers/gcp.md)")
		}
	}
	return nil
}

func requireProviderVariables(provider string, keys ...string) error {
	var missing []string
	for _, key := range keys {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: missing %s; configure CI variables as described in docs/providers/%s.md", provider, strings.Join(missing, ", "), provider)
	}
	return nil
}

func (p infraProvider) Authenticate(ctx context.Context, root string, ci bool, stdin io.Reader, stdout, stderr io.Writer) (map[string]string, error) {
	if p.name != "gcp" {
		return nil, p.EnsureEnvironment(ci)
	}
	runner := infraCommandRunner{stdin: stdin, stdout: stdout, stderr: stderr}
	if !ci {
		if err := runner.run(ctx, root, nil, "gcloud", "auth", "application-default", "login"); err != nil {
			return nil, err
		}
		return nil, runner.run(ctx, root, nil, "gcloud", "auth", "login")
	}
	if path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
		if err := p.EnsureEnvironment(true); err != nil {
			return nil, err
		}
		return map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": path, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE": path}, nil
	}
	if err := requireProviderVariables("gcp", "GCP_WORKLOAD_IDENTITY_PROVIDER", "GCP_SERVICE_ACCOUNT"); err != nil {
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
		if err := githubIdentityToken(ctx, audience, token); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(directory, "credentials.json")
	if err := runner.run(ctx, root, nil, "gcloud", "iam", "workload-identity-pools", "create-cred-config", os.Getenv("GCP_WORKLOAD_IDENTITY_PROVIDER"), "--service-account="+os.Getenv("GCP_SERVICE_ACCOUNT"), "--credential-source-file="+token, "--output-file="+path); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	success = true
	return map[string]string{"GOOGLE_APPLICATION_CREDENTIALS": path, "CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE": path}, nil
}

// GitHub's runner token endpoint is an adapter. Other runners supply CI_OIDC_TOKEN_FILE.
func githubIdentityToken(ctx context.Context, audience, path string) error {
	endpoint := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	bearer := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if endpoint == "" || bearer == "" {
		return errors.New("gcp: supply CI_OIDC_TOKEN_FILE from the CI issuer (see docs/providers/gcp.md)")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" {
		return errors.New("gcp: invalid CI identity endpoint")
	}
	query := u.Query()
	query.Set("audience", audience)
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("gcp: invalid CI identity request")
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("gcp: CI identity exchange failed")
	}
	defer response.Body.Close()
	var identity struct {
		Value string `json:"value"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&identity) != nil || identity.Value == "" {
		return errors.New("gcp: CI issuer did not return an identity token")
	}
	return os.WriteFile(path, []byte(identity.Value), 0600)
}

func runningInCI() bool {
	return os.Getenv("CI") == "true" || os.Getenv("GITHUB_ACTIONS") == "true" || os.Getenv("GITLAB_CI") == "true"
}
