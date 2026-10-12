package core

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestSecretProfilesDoNotFallbackOrFetchOtherKeys(t *testing.T) {
	root := t.TempDir()
	manifest := NewManifest("secrets-test")
	manifest.Workspace.Projects = []Project{{Name: "web", Template: "registry:web", Path: "apps/web", Secrets: &ProjectSecrets{Config: "fnox.toml", Profiles: map[string]string{"dev": "dev", "prod": "prod"}, Keys: []string{"DATABASE_URL"}}}}
	if err := os.MkdirAll(filepath.Join(root, "apps/web"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	config := "[profiles.dev.secrets]\nDATABASE_URL = { value = 'development' }\nOTHER_SECRET = { value = 'unrequested' }\n[profiles.prod.secrets]\nDATABASE_URL = { value = 'production' }\n"
	if err := os.WriteFile(filepath.Join(root, "apps/web/fnox.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	reader := &fixtureSecretReader{value: "development"}
	values, err := loadProjectSecrets(context.Background(), root, "web", "dev", "", reader)
	if err != nil {
		t.Fatal(err)
	}
	if values["DATABASE_URL"] != "development" || len(reader.keys) != 1 || reader.keys[0] != "DATABASE_URL" {
		t.Fatalf("unexpected key selection: %v", reader.keys)
	}
	if _, err := loadProjectSecrets(context.Background(), root, "web", "stage", "", reader); err == nil {
		t.Fatal("undeclared environment used fallback")
	}
	reader.value = "rotated-application-value"
	refreshed, err := loadProjectSecrets(t.Context(), root, "web", "dev", "", reader)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed["DATABASE_URL"] != "rotated-application-value" {
		t.Fatal("refresh reused the previous value")
	}
	reader.err = errors.New("provider denied access")
	if _, err := loadProjectSecrets(context.Background(), root, "web", "dev", "", reader); err == nil {
		t.Fatal("provider error ignored")
	}
}

func TestSecretDeclarationsRejectAdministrationKeysAndUnsafePaths(t *testing.T) {
	for _, settings := range []*ProjectSecrets{
		{Config: "../fnox.toml", Profiles: map[string]string{"dev": "dev"}, Keys: []string{"DATABASE_URL"}},
		{Config: "fnox.toml", Profiles: map[string]string{"dev": "dev"}, Keys: []string{"VERCEL_TOKEN"}},
		{Config: "fnox.toml", Profiles: map[string]string{"dev": "dev"}, Keys: []string{"DATABASE_URL"}, File: "../.env.local"},
	} {
		if err := validateProjectSecrets(Project{Secrets: settings}); err == nil {
			t.Fatal("unsafe declaration accepted")
		}
	}
}

func TestPrivateSecretFilesAndDotenvEncoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.local")
	data := encodeSecretEnv(map[string]string{"MULTILINE": "first\nsecond", "QUOTES": "a\"b\\c", "DOLLAR": "$HOME"})
	if !strings.Contains(string(data), `MULTILINE="first\nsecond"`) || !strings.Contains(string(data), `DOLLAR="$HOME"`) {
		t.Fatal("incorrect dotenv encoding")
	}
	if err := writePrivateSecretFile(path, data); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("secret file is not user-only")
	}
}

func TestAppEnvironmentDropsAdminTokens(t *testing.T) {
	result := appSecretEnvironment([]string{"VERCEL_TOKEN=admin", "NEON_API_KEY=admin", "DATABASE_URL=old", "PATH=/bin"}, map[string]string{"DATABASE_URL": "new"})
	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "admin") || strings.Contains(joined, "old") || !strings.Contains(joined, "DATABASE_URL=new") {
		t.Fatal("incorrect secret injection")
	}
}

func TestSecretRedactionAcrossOutputWrites(t *testing.T) {
	var output bytes.Buffer
	writer := secretRedactor{destination: &output, values: map[string]string{"DATABASE_URL": "sensitive-value"}}
	_, _ = writer.Write([]byte("result: sensitive-"))
	_, _ = writer.Write([]byte("value\n"))
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "result: [REDACTED]\n" {
		t.Fatal("split output exposed a secret")
	}
}

func TestFnoxResolverDoesNotReceiveAdministrationCredentials(t *testing.T) {
	root := t.TempDir()
	script := `#!/bin/sh
[ -z "$NEON_API_KEY" ] && [ -z "$VERCEL_TOKEN" ] && [ -z "$PULUMI_CONFIG_PASSPHRASE" ] || exit 1
[ -z "$FNOX_PROFILE" ] && [ -z "$RUST_LOG" ] || exit 1
printf '{"schema":1,"set":{"APP_SECRET":"application-value"}}\n'
`
	if err := os.WriteFile(filepath.Join(root, "mise"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, key := range []string{"NEON_API_KEY", "VERCEL_TOKEN", "PULUMI_CONFIG_PASSPHRASE", "FNOX_PROFILE", "RUST_LOG"} {
		t.Setenv(key, "administration-only")
	}
	value, err := (commandSecretReader{}).Read(t.Context(), root, "fnox.toml", "dev", []string{"APP_SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if value["APP_SECRET"] != "application-value" {
		t.Fatal("application value was not resolved")
	}
}

// Private helpers ------------------------------------------------------------

type fixtureSecretReader struct {
	value string
	keys  []string
	err   error
}

func (reader *fixtureSecretReader) Read(_ context.Context, _, _, _ string, keys []string) (map[string]string, error) {
	reader.keys = append(reader.keys, keys...)
	values := map[string]string{}
	for _, key := range keys {
		values[key] = reader.value
	}
	return values, reader.err
}
