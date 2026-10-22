package providers_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/providers"
)

// Public API -----------------------------------------------------------------

func TestGCPEnvironmentRequiresCredentialsAndAcceptsExistingADC(t *testing.T) {
	// Arrange.
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	provider, ok := providers.InfrastructureProvider("gcp")
	if !ok {
		t.Fatal("missing GCP provider")
	}

	// Act and assert.
	if err := provider.EnsureEnvironment(true); err == nil || !strings.Contains(err.Error(), "docs/providers/gcp.md") {
		t.Fatalf("missing helpful failure: %v", err)
	}
	path := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", path)
	if err := provider.EnsureEnvironment(true); err != nil {
		t.Fatal(err)
	}
}

func TestNativeGCPAuthPrecedesRootFallback(t *testing.T) {
	root := t.TempDir()
	manifest := core.NewManifest("auth")
	manifest.Workspace.Infra = &core.Infrastructure{Providers: []string{"gcp", "custom"}}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
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
	values, err := core.AuthenticateInfrastructure(t.Context(), root, true, nil, nil, nil)
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
	if _, err := core.AuthenticateInfrastructure(t.Context(), root, true, nil, nil, nil); err == nil {
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
	if _, err := core.AuthenticateInfrastructure(t.Context(), root, true, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "root mise.toml") {
		t.Fatalf("missing root fallback accepted: %v", err)
	}
}
