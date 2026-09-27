package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCargoPackage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		found   bool
		publish bool
		wantErr string
	}{
		{name: "virtual workspace", content: "[workspace]\n", found: false},
		{name: "default registry", content: "[package]\nname = 'demo'\nversion = '1.0.0'\n", found: true, publish: true},
		{name: "private package", content: "[package]\nname = 'demo'\nversion = '1.0.0'\npublish = false\n", found: true},
		{name: "other registry", content: "[package]\nname = 'demo'\nversion = '1.0.0'\npublish = ['custom']\n", found: true},
		{name: "crates.io allowlist", content: "[package]\nname = 'demo'\nversion = '1.0.0'\npublish = ['custom', 'crates-io']\n", found: true, publish: true},
		{name: "missing version", content: "[package]\nname = 'demo'\n", wantErr: "no [package] version"},
		{name: "bad publish value", content: "[package]\nname = 'demo'\nversion = '1.0.0'\npublish = 12\n", wantErr: "invalid [package] publish"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Cargo.toml")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			pkg, found, err := ReadCargoPackage(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ReadCargoPackage error = %v; want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || found != tc.found {
				t.Fatalf("ReadCargoPackage = %+v, %v, %v; want found=%v", pkg, found, err, tc.found)
			}
			if found && (pkg.Name != "demo" || pkg.Version != "1.0.0" || pkg.PublishCratesIO != tc.publish) {
				t.Fatalf("ReadCargoPackage = %+v; want publish=%v", pkg, tc.publish)
			}
		})
	}
	if _, found, err := ReadCargoPackage(filepath.Join(t.TempDir(), "missing.toml")); err != nil || found {
		t.Fatalf("missing manifest: found=%v, err=%v", found, err)
	}
}

func TestSetCargoPackageVersionPreservesOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cargo.toml")
	before := "[package]\nname = 'demo'\nversion = '1.0.0' # release\n\n[dependencies]\nversion = '5.0'\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetCargoPackageVersion(path, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(before, "version = '1.0.0'", "version = \"1.2.3\"", 1)
	if string(data) != want {
		t.Fatalf("manifest = %q; want %q", data, want)
	}
}
