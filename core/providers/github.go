package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Private helpers ------------------------------------------------------------

var githubSecretKeys = []string{"GH_TOKEN", "GITHUB_TOKEN"}

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

func syncGitHubSecret(ctx context.Context, root, repository, environment, key, value string) error {
	command := exec.CommandContext(ctx, "gh", "secret", "set", key, "--repo", repository, "--env", environment)
	command.Dir = root
	command.Stdin = strings.NewReader(value)
	if err := command.Run(); err != nil {
		return fmt.Errorf("sync GitHub secret %s failed; previous keys can already be updated", key)
	}
	return nil
}
