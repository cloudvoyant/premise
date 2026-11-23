package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestStackNamesSeparateEnvironmentsAndAvoidPreviewCollisions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options InfraOptions
		want    string
		valid   bool
	}{
		{"development", InfraOptions{Environment: "dev"}, "dev", true},
		{"staging", InfraOptions{Environment: "stage"}, "stage", true},
		{"production", InfraOptions{Environment: "prod"}, "prod", true},
		{"shared", InfraOptions{Environment: "shared"}, "shared", true},
		{"missing preview branch", InfraOptions{Environment: "preview"}, "", false},
		{"branch on production", InfraOptions{Environment: "prod", Branch: "feature/a"}, "", false},
		{"branch on shared", InfraOptions{Environment: "shared", Branch: "feature/a"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveStackName(tc.options)
			if (err == nil) != tc.valid || got != tc.want {
				t.Fatalf("got %q, %v; want %q, valid=%t", got, err, tc.want, tc.valid)
			}
		})
	}
	a, _ := ResolveStackName(InfraOptions{Environment: "preview", Branch: "feature/a"})
	repeated, _ := ResolveStackName(InfraOptions{Environment: "preview", Branch: "feature/a"})
	b, _ := ResolveStackName(InfraOptions{Environment: "preview", Branch: "feature-a"})
	otherType, _ := ResolveStackName(InfraOptions{Environment: "preview", Branch: "fix/a"})
	if !strings.HasPrefix(a, "preview-a-") || a == otherType {
		t.Fatal("preview names must omit the type prefix and retain distinct branch identities")
	}
	if a != repeated || a == b {
		t.Fatal("preview identity is unstable or collides")
	}
}

func TestBackendValidationAllowsRemoteStoresAndRequiresExplicitLocalAccess(t *testing.T) {
	for _, tc := range []struct {
		url               string
		allowLocal, valid bool
	}{
		{"gs://state", false, true}, {"s3://state", false, true}, {"azblob://state", false, true},
		{"https://app.pulumi.com", false, false}, {"gs://", false, false},
		{"gs://user:password@bucket", false, false}, {"gs://state?token=credential", false, false},
		{"file:///tmp/state", false, false}, {"file:///tmp/state", true, true},
	} {
		t.Run(tc.url, func(t *testing.T) {
			err := validateBackend(InfraBackend{URL: tc.url, AllowLocal: tc.allowLocal})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t: %v", tc.valid, err)
			}
		})
	}
}

func TestPreviewCleanupHandlesMissingStacksWithoutHidingBackendFailures(t *testing.T) {
	for _, tc := range []struct {
		name, listing, status string
		wantDestroy, wantErr  bool
	}{
		{"already removed", `[]`, "0", false, false},
		{"another environment", `[{"name":"prod"}]`, "0", false, false},
		{"matching preview", "matching", "0", true, false},
		{"qualified preview", "qualified", "0", true, false},
		{"backend denied", `[]`, "1", false, true},
		{"invalid response", `not json`, "0", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			prepareInfraTestProject(t, root)
			if err := os.MkdirAll(filepath.Join(root, "infra/environment"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := SaveManifest(filepath.Join(root, ManifestFilename), infraTestManifest()); err != nil {
				t.Fatal(err)
			}
			options := InfraOptions{Environment: "preview", Branch: "feature/cleanup"}
			stack, err := ResolveStackName(options)
			if err != nil {
				t.Fatal(err)
			}
			listing := tc.listing
			if listing == "matching" {
				listing = `[{"name":"` + stack + `"}]`
			}
			if listing == "qualified" {
				listing = `[{"name":"organization/environment/` + stack + `"}]`
			}
			bin := t.TempDir()
			log := filepath.Join(t.TempDir(), "destroyed")
			script := "#!/bin/sh\ncase \"$1 $2\" in\n  'stack ls') printf '%s' \"$STACK_LIST\"; exit \"$LIST_STATUS\";;\nesac\nif [ \"$1\" = destroy ]; then touch \"$DESTROY_LOG\"; fi\n"
			if err := os.WriteFile(filepath.Join(bin, "pulumi"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "mise"), []byte("#!/bin/sh\n[ \"$1 $2 $3\" = 'exec -- pulumi' ] || exit 9\nshift 3\nexec pulumi \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("STACK_LIST", listing)
			t.Setenv("LIST_STATUS", tc.status)
			t.Setenv("DESTROY_LOG", log)
			err = RunInfra(t.Context(), root, "destroy", options, nil, nil, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("cleanup error = %v", err)
			}
			_, err = os.Stat(log)
			if (err == nil) != tc.wantDestroy {
				t.Fatalf("destroyed=%t, want %t", err == nil, tc.wantDestroy)
			}
		})
	}
}

func TestInfrastructureRuntimeSecretsDoNotLeakThroughLogs(t *testing.T) {
	directory := t.TempDir()
	script := filepath.Join(directory, "pulumi")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$APP_MARKER\" \"$GH_TOKEN\"\nprintf '%s\\n' \"$APP_MARKER\" >&2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "administration-token-test")
	var output, diagnostics bytes.Buffer
	r := infraCommandRunner{stdout: &output, stderr: &diagnostics, secrets: map[string]string{"APP_MARKER": "runtime-secret-test"}}
	if err := r.run(context.Background(), directory, map[string]string{"APP_MARKER": "runtime-secret-test"}, "pulumi", "preview"); err != nil {
		t.Fatal(err)
	}
	text := output.String() + diagnostics.String()
	if strings.Contains(text, "runtime-secret-test") || strings.Contains(text, "administration-token-test") {
		t.Fatal("runtime/provider credential leaked")
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatal("no redaction evidence")
	}
}

func TestInfrastructureScaffoldingAndPathResolution(t *testing.T) {
	t.Run("when scaffolding infra", func(t *testing.T) {
		scaffold := WorkspaceScaffold{
			MiseTemplate: "monorepo_root = true\n",
			WorkflowAssets: []WorkflowAsset{
				{Path: ".github/workflows/on-commit.yml", Content: "name: On Commit\n"},
				{Path: ".github/workflows/on-deploy.yml", Content: "name: On Deploy\n", InfrastructureOnly: true},
			},
		}

		t.Run("given infra/ is absent, then it stages infra/env and infra/shared and writes both to premise.yaml", func(t *testing.T) {
			// Arrange.
			root := filepath.Join(t.TempDir(), "sample-app")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			installFakePulumiMise(t)

			// Act.
			manifestPath, err := InitializeWorkspaceWithInfrastructure(t.Context(), root, scaffold, InfrastructureSetup{Language: "go"}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}

			// Assert.
			manifest, err := LoadManifest(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Workspace.Infra == nil {
				t.Fatal("workspace.infra was not written")
			}
			if manifest.Workspace.Infra.Directory != "infra/env" || manifest.Workspace.Infra.SharedDirectory != "infra/shared" {
				t.Fatalf("infra directories = (%q, %q), want (infra/env, infra/shared)", manifest.Workspace.Infra.Directory, manifest.Workspace.Infra.SharedDirectory)
			}
			if _, ok := manifest.Workspace.Infra.Environments["shared"]; !ok {
				t.Fatalf("environments missing shared: %#v", manifest.Workspace.Infra.Environments)
			}
			envPulumi, err := os.ReadFile(filepath.Join(root, "infra/env/Pulumi.yaml"))
			if err != nil || !strings.Contains(string(envPulumi), "name: sample-app\n") {
				t.Fatalf("unexpected infra/env/Pulumi.yaml: %v\n%s", err, envPulumi)
			}
			sharedPulumi, err := os.ReadFile(filepath.Join(root, "infra/shared/Pulumi.yaml"))
			if err != nil || !strings.Contains(string(sharedPulumi), "name: sample-app-shared\n") {
				t.Fatalf("unexpected infra/shared/Pulumi.yaml: %v\n%s", err, sharedPulumi)
			}
			miseData, err := os.ReadFile(filepath.Join(root, "mise.toml"))
			if err != nil || !strings.Contains(string(miseData), "infra/shared") || !strings.Contains(string(miseData), "infra/env") {
				t.Fatalf("unexpected mise.toml: %v\n%s", err, miseData)
			}
		})

		t.Run("given anything exists in infra/, then it rejects and leaves files unchanged", func(t *testing.T) {
			// Arrange.
			root := filepath.Join(t.TempDir(), "existing-app")
			if err := os.MkdirAll(filepath.Join(root, "infra"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := InitializeWorkspace(root, scaffold.MiseTemplate, scaffold.WorkflowAssets...); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(root, "infra", "notes.txt")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			beforeManifest, err := os.ReadFile(filepath.Join(root, ManifestFilename))
			if err != nil {
				t.Fatal(err)
			}
			beforeMise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
			if err != nil {
				t.Fatal(err)
			}
			installFakePulumiMise(t)

			// Act.
			err = SetupInfrastructure(t.Context(), root, scaffold, InfrastructureSetup{Language: "go"}, nil, nil, nil)

			// Assert.
			if err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("expected existing infra error, got: %v", err)
			}
			afterManifest, _ := os.ReadFile(filepath.Join(root, ManifestFilename))
			afterMise, _ := os.ReadFile(filepath.Join(root, "mise.toml"))
			afterSentinel, _ := os.ReadFile(sentinel)
			if string(afterManifest) != string(beforeManifest) || string(afterMise) != string(beforeMise) || string(afterSentinel) != "keep" {
				t.Fatal("failed setup modified workspace files")
			}
		})
	})

	t.Run("when resolving infra paths", func(t *testing.T) {
		t.Run("given no explicit directories, then env resolves to infra/env and shared resolves to infra/shared", func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writePulumiYAML(t, filepath.Join(root, "infra/env"), "app")
			writePulumiYAML(t, filepath.Join(root, "infra/shared"), "app-shared")
			manifest := NewManifest("app")
			manifest.Workspace.Infra = &Infrastructure{
				Backend:  "remote",
				Backends: map[string]InfraBackend{"remote": {URL: "gs://state"}},
			}
			if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
				t.Fatal(err)
			}

			// Act.
			envRes, err := ResolveInfrastructure(root, InfraOptions{Environment: "dev"})
			if err != nil {
				t.Fatal(err)
			}
			sharedRes, err := ResolveInfrastructure(root, InfraOptions{Environment: "shared"})
			if err != nil {
				t.Fatal(err)
			}

			// Assert.
			if envRes.Directory != filepath.Join(root, "infra/env") || envRes.ProjectName != "app" {
				t.Fatalf("env resolution = %#v", envRes)
			}
			if sharedRes.Directory != filepath.Join(root, "infra/shared") || sharedRes.ProjectName != "app-shared" || sharedRes.Stack != "shared" {
				t.Fatalf("shared resolution = %#v", sharedRes)
			}
		})

		t.Run("given legacy directory: infra, then env resolves to infra/", func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writePulumiYAML(t, filepath.Join(root, "infra"), "legacy")
			manifest := NewManifest("legacy")
			manifest.Workspace.Infra = &Infrastructure{
				Directory: "infra",
				Backend:   "remote",
				Backends:  map[string]InfraBackend{"remote": {URL: "gs://state"}},
			}
			if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
				t.Fatal(err)
			}

			// Act.
			envRes, err := ResolveInfrastructure(root, InfraOptions{Environment: "dev"})
			if err != nil {
				t.Fatal(err)
			}
			hasEnv, hasShared := InfrastructurePresence(root, manifest.Workspace.Infra)

			// Assert.
			if envRes.Directory != filepath.Join(root, "infra") || envRes.ProjectName != "legacy" {
				t.Fatalf("legacy env resolution = %#v", envRes)
			}
			if !hasEnv || hasShared {
				t.Fatalf("presence = (env=%t, shared=%t), want (true, false)", hasEnv, hasShared)
			}
		})
	})
}

// Private helpers ------------------------------------------------------------

func writePulumiYAML(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "Pulumi.yaml"), []byte("name: "+name+"\nruntime: go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func installFakePulumiMise(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
set -eu
if [ "$1 $2 $3 $4" = "exec -- pulumi new" ]; then
  shift 5
  name=""
  dir=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --name) name="$2"; shift 2 ;;
      --dir) dir="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  mkdir -p "$dir"
  printf 'name: %s\nruntime: go\n' "$name" > "$dir/Pulumi.yaml"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func infraTestManifest() Config {
	manifest := NewManifest("example")
	manifest.Workspace.Infra = &Infrastructure{Backend: "remote", Backends: map[string]InfraBackend{"remote": {URL: "gs://test"}}, Environments: map[string]InfraEnvironment{"dev": {}}}
	manifest.Workspace.Infra.Directory = "infra/environment"
	return manifest
}

type recordingInfraRunner struct{ commands [][]string }

func (r *recordingInfraRunner) run(_ context.Context, _ string, _ map[string]string, name string, args ...string) error {
	r.commands = append(r.commands, append([]string{name}, args...))
	return nil
}

func prepareInfraTestProject(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "infra/environment")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "Pulumi.yaml"), []byte("name: environment\nruntime: yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte(`[tools]
pulumi = "3.268.0"
`), 0600); err != nil {
		t.Fatal(err)
	}
}
