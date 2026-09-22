package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeUpdateVersion(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "", want: ""},
		{input: "0.2.1", want: "v0.2.1"},
		{input: "v1.2.3", want: "v1.2.3"},
		{input: "v1.2.3-rc.4", want: "v1.2.3-rc.4"},
	} {
		t.Run(test.input, func(t *testing.T) {
			got, err := normalizeUpdateVersion(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("normalizeUpdateVersion(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
	if _, err := normalizeUpdateVersion("next"); err == nil {
		t.Fatal("expected invalid version error")
	}
}

func TestUpdatePremiseRunsInstallerForCurrentExecutableDirectory(t *testing.T) {
	installDirectory := t.TempDir()
	target := filepath.Join(installDirectory, "premise")
	if err := os.WriteFile(target, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(installDirectory, "pm")
	if err := os.Symlink("premise", alias); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "installer-environment")
	t.Setenv("CAPTURE", capture)
	t.Setenv("INSTALL_DIR", "/wrong/directory")
	t.Setenv("VERSION", "v9.9.9")

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("#!/usr/bin/env bash\nset -eu\nprintf '%s|%s\\n' \"$INSTALL_DIR\" \"$VERSION\" > \"$CAPTURE\"\necho updated\n"))
	}))
	defer server.Close()

	var output bytes.Buffer
	err := updatePremise(t.Context(), "v1.2.3", server.URL, server.Client(), func() (string, error) {
		return alias, nil
	}, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	canonicalInstallDirectory, err := filepath.EvalSymlinks(installDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), canonicalInstallDirectory+"|v1.2.3\n"; got != want {
		t.Fatalf("installer environment = %q, want %q", got, want)
	}
	if !strings.Contains(output.String(), "updated") {
		t.Fatalf("update output = %q", output.String())
	}
}

func TestUpdatePremiseReportsDownloadFailure(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "premise")
	if err := os.WriteFile(executable, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := updatePremise(t.Context(), "", server.URL, server.Client(), func() (string, error) {
		return executable, nil
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("updatePremise() error = %v", err)
	}

	err = updatePremise(t.Context(), "", server.URL, server.Client(), func() (string, error) {
		return "", errors.New("missing executable")
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "missing executable") {
		t.Fatalf("updatePremise() executable error = %v", err)
	}
}

func TestUpdateEnvironmentClearsInheritedInstallerSettings(t *testing.T) {
	environment := updateEnvironment([]string{"PATH=/bin", "INSTALL_DIR=/wrong", "VERSION=v9.9.9"}, "/current/bin", "")
	want := []string{"PATH=/bin", "INSTALL_DIR=/current/bin", "VERSION="}
	if strings.Join(environment, "\n") != strings.Join(want, "\n") {
		t.Fatalf("update environment = %#v, want %#v", environment, want)
	}
}
