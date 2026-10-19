package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestInfrastructureProviderEnvironment(t *testing.T) {
	for _, name := range []string{"gcp", "neon", "vercel"} {
		t.Run(name, func(t *testing.T) {
			for _, key := range []string{"GOOGLE_APPLICATION_CREDENTIALS", "NEON_API_KEY", "VERCEL_API_TOKEN"} {
				t.Setenv(key, "")
			}
			provider, ok := InfrastructureProvider(name)
			if !ok {
				t.Fatal("missing provider")
			}
			if err := provider.EnsureEnvironment(true); err == nil || !strings.Contains(err.Error(), "docs/providers/"+name+".md") {
				t.Fatalf("missing helpful failure: %v", err)
			}
			switch name {
			case "gcp":
				path := filepath.Join(t.TempDir(), "adc.json")
				os.WriteFile(path, []byte("{}"), 0600)
				t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
			case "neon":
				t.Setenv("NEON_API_KEY", "ci-value")
			case "vercel":
				t.Setenv("VERCEL_API_TOKEN", "ci-value")
			}
			if err := provider.EnsureEnvironment(true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeGCPAuthPrecedesRootFallback(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("auth")
	manifest.Workspace.Infra = &Infrastructure{Providers: []string{"gcp", "custom"}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tasks.\"ci:auth\"]\nrun = 'fallback'\n"), 0600)
	token := filepath.Join(root, "oidc")
	os.WriteFile(token, []byte("test-identity"), 0600)
	log := filepath.Join(root, "log")
	t.Setenv("AUTH_TEST_LOG", log)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	t.Setenv("CI_OIDC_TOKEN_FILE", token)
	t.Setenv("GCP_WORKLOAD_IDENTITY_PROVIDER", "projects/1/locations/global/workloadIdentityPools/test/providers/test")
	t.Setenv("GCP_SERVICE_ACCOUNT", "test@example.invalid")
	os.WriteFile(filepath.Join(root, "gcloud"), []byte(`#!/bin/sh
printf 'native\n' >> "$AUTH_TEST_LOG"
for arg in "$@"; do case "$arg" in --output-file=*) printf '{}' > "${arg#--output-file=}";; esac; done
`), 0700)
	os.WriteFile(filepath.Join(root, "mise"), []byte(`#!/bin/sh
[ "$*" = 'run ci:auth' ] || exit 9
[ -f "$GOOGLE_APPLICATION_CREDENTIALS" ] || exit 10
printf 'fallback\n' >> "$AUTH_TEST_LOG"
`), 0700)
	values, err := AuthenticateInfrastructure(t.Context(), root, true, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(values["GOOGLE_APPLICATION_CREDENTIALS"]))
	data, _ := os.ReadFile(log)
	if string(data) != "native\nfallback\n" {
		t.Fatalf("auth order = %s", data)
	}
	if values["CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE"] != values["GOOGLE_APPLICATION_CREDENTIALS"] {
		t.Fatal("gcloud and Pulumi use different identities")
	}
	info, _ := os.Stat(values["GOOGLE_APPLICATION_CREDENTIALS"])
	if info.Mode().Perm() != 0600 {
		t.Fatal("credential file is not private")
	}
	// Native authentication failures must stop before the fallback.
	os.WriteFile(filepath.Join(root, "log"), nil, 0600)
	os.WriteFile(filepath.Join(root, "gcloud"), []byte("#!/bin/sh\nexit 1\n"), 0700)
	if _, err := AuthenticateInfrastructure(t.Context(), root, true, nil, nil, nil); err == nil {
		t.Fatal("native auth failure ignored")
	}
	data, _ = os.ReadFile(log)
	if len(data) != 0 {
		t.Fatal("fallback ran after failed native auth")
	}
	// Existing ADC bypasses interactive login and federation setup.
	adc := filepath.Join(root, "existing-adc.json")
	os.WriteFile(adc, []byte("{}"), 0600)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", adc)

	// Child/project/global tasks cannot accidentally become a root fallback.
	os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tasks.other]\nrun = 'echo other'\n"), 0600)
	if _, err := AuthenticateInfrastructure(t.Context(), root, true, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "root mise.toml") {
		t.Fatalf("missing root fallback accepted: %v", err)
	}
}

func TestCIApplicationSecretsUseEnvironmentWithoutFnox(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("ci-secrets")
	manifest.Workspace.Projects = []Project{{Name: "web", Template: "registry:web", Path: "apps/web", Secrets: &ProjectSecrets{Config: "fnox.toml", Profiles: map[string]string{"stage": "stage"}, Keys: []string{"APP_SECRET"}}}}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CI", "true")
	t.Setenv("APP_SECRET", "from-ci")
	t.Setenv("PATH", t.TempDir()) // No fnox, Mise, or cloud CLI is available.
	values, err := LoadProjectSecrets(t.Context(), root, "web", "stage", "")
	if err != nil || values["APP_SECRET"] != "from-ci" {
		t.Fatalf("CI did not supply values: %v", err)
	}
	t.Setenv("APP_SECRET", "")
	if _, err := LoadProjectSecrets(t.Context(), root, "web", "stage", ""); err == nil || !strings.Contains(err.Error(), "APP_SECRET") {
		t.Fatal("missing CI variable accepted")
	}
}

func TestSecretSyncKeepsValuesOutOfArguments(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("sync")
	manifest.Workspace.Projects = []Project{{Name: "web", Template: "registry:web", Path: "apps/web", Secrets: &ProjectSecrets{Config: "fnox.toml", Profiles: map[string]string{"stage": "stage"}, Keys: []string{"APP_SECRET"}}}}
	os.MkdirAll(filepath.Join(root, "apps/web"), 0700)
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "mise"), []byte("#!/bin/sh\nprintf '%s' '{\"schema\":1,\"set\":{\"APP_SECRET\":\"secret-value\"}}'\n"), 0700)
	os.WriteFile(filepath.Join(root, "gh"), []byte(`#!/bin/sh
[ "$*" = 'secret set APP_SECRET --repo owner/repo --env stage' ] || exit 2
[ "$(cat)" = secret-value ] || exit 3
`), 0700)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := SyncProjectSecrets(t.Context(), root, "web", "stage", "", "github", "owner/repo"); err != nil {
		t.Fatal(err)
	}
	// Diagnostics from a failed secret write must not reveal stdin or provider output.
	os.WriteFile(filepath.Join(root, "gh"), []byte("#!/bin/sh\ncat >&2\nexit 1\n"), 0700)
	err := SyncProjectSecrets(t.Context(), root, "web", "stage", "", "github", "owner/repo")
	if err == nil || bytes.Contains([]byte(err.Error()), []byte("secret-value")) {
		t.Fatalf("unsafe sync failure: %v", err)
	}
}
