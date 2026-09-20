package e2e_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	core "github.com/cloudvoyant/premise/core"
)

type fixedQuestionnaire struct {
	Answers map[string]string
}

func (questionnaire fixedQuestionnaire) Ask(_ []core.Question) (map[string]string, error) {
	answers := make(map[string]string, len(questionnaire.Answers))
	for name, value := range questionnaire.Answers {
		answers[name] = value
	}
	return answers, nil
}

type fixedMergeResolver struct {
	Decisions map[string]core.MergeDecision
	Calls     []core.MergeConflict
}

func (resolver *fixedMergeResolver) ResolveMergeConflict(conflict core.MergeConflict) (core.MergeDecision, error) {
	resolver.Calls = append(resolver.Calls, conflict)
	if decision, ok := resolver.Decisions[conflict.Key]; ok {
		return decision, nil
	}
	if decision, ok := resolver.Decisions[conflict.Path]; ok {
		return decision, nil
	}
	return core.MergeDecision{Choice: core.MergeChoiceAbort}, nil
}

func fixedOptions(answers map[string]string) core.GenerateOptions {
	return core.GenerateOptions{
		Questionnaire:    fixedQuestionnaire{Answers: answers},
		ConflictResolver: (&fixedMergeResolver{Decisions: map[string]core.MergeDecision{}}).ResolveMergeConflict,
	}
}

type miseInvocation struct {
	Directory   string
	Environment string
	Arguments   string
}

type miseInvocationLog struct {
	Path string
}

func (log *miseInvocationLog) Invocations(t *testing.T) []miseInvocation {
	t.Helper()
	data, err := os.ReadFile(log.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	invocations := make([]miseInvocation, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			t.Fatalf("malformed Mise invocation %q", line)
		}
		invocations = append(invocations, miseInvocation{Directory: parts[0], Environment: parts[1], Arguments: parts[2]})
	}
	return invocations
}

func installMiseTaskShim(t *testing.T) *miseInvocationLog {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := &miseInvocationLog{Path: filepath.Join(t.TempDir(), "mise.log")}
	script := "#!/bin/sh\n" +
		"printf '%s|%s|%s\\n' \"$PWD\" \"$PREMISE_TEMPLATE_TEST\" \"$*\" >> \"$MISE_LOG\"\n" +
		"if [ -n \"$MISE_FAIL_DIRECTORY\" ]; then case \"$PWD\" in *\"$MISE_FAIL_DIRECTORY\"*) if [ -z \"$MISE_FAIL_ARGUMENT\" ] || [ \"$*\" = \"$MISE_FAIL_ARGUMENT\" ]; then exit 7; fi;; esac; fi\n" +
		"if [ -n \"$MISE_FAIL_MISE_CONTAINS\" ] && [ -f mise.toml ] && grep -F \"$MISE_FAIL_MISE_CONTAINS\" mise.toml >/dev/null; then exit 8; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISE_LOG", log.Path)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func workspaceMiseTemplate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "templates", "mise.toml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workspace mise template: %v", err)
	}
	return string(content)
}

func TestWorkspaceTemplateAndGenerationWorkflow(t *testing.T) {
	installMiseTaskShim(t)
	base := t.TempDir()
	root := filepath.Join(base, "example")
	registryRoot := filepath.Join(base, "registry")
	for _, directory := range []string{root, registryRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := core.SaveManifest(filepath.Join(registryRoot, core.ManifestFilename), core.NewManifest("registry")); err != nil {
		t.Fatal(err)
	}
	workspaceTemplate := workspaceMiseTemplate(t)
	manifestPath, err := core.InitializeWorkspace(root, workspaceTemplate)
	if err != nil {
		t.Fatal(err)
	}
	workspaceMise, err := os.ReadFile(filepath.Join(root, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(workspaceMise) != workspaceTemplate {
		t.Fatalf("workspace mise.toml does not match the canonical template:\n%s", workspaceMise)
	}
	if _, err := core.InitializeWorkspace(root, workspaceTemplate); err == nil {
		t.Fatal("expected existing manifest error")
	}
	templatePath, err := core.InitializeTemplate(registryRoot, "app")
	if err != nil {
		t.Fatal(err)
	}
	misePath := filepath.Join(templatePath, "mise.toml")
	if err := os.Chmod(misePath, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := []byte{'p', 'r', 'e', 'm', 'i', 's', 'e', '-', 'a', 'p', 'p', 0, 1}
	if err := os.WriteFile(filepath.Join(templatePath, "asset.bin"), binary, 0o644); err != nil {
		t.Fatal(err)
	}

	registry, err := core.LoadRegistry(registryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if names := registry.Names(); len(names) != 1 || names[0] != "app" {
		t.Fatalf("unexpected registry names: %v", names)
	}
	var output bytes.Buffer
	if err := core.Generate(context.Background(), root, "../registry:app", fixedOptions(map[string]string{"name": "orders"}), &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Generated app orders") {
		t.Fatalf("unexpected generation output: %s", output.String())
	}
	destination := filepath.Join(root, "apps", "orders")
	source, err := core.TemplateDirectory(registryRoot, "app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.BuildMergePlan(core.MergeRequest{SelectedTemplateRoot: source, Destination: destination}); err == nil {
		t.Fatal("expected existing destination error")
	}

	generated, err := os.ReadFile(filepath.Join(destination, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "orders: build") || strings.Contains(string(generated), "premise-app") {
		t.Fatalf("unexpected generated template:\n%s", generated)
	}
	copiedBinary, err := os.ReadFile(filepath.Join(destination, "asset.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copiedBinary, binary) {
		t.Fatalf("binary changed: %v", copiedBinary)
	}
	info, err := os.Stat(filepath.Join(destination, "mise.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}

	reloaded, err := core.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Workspace.Projects) != 1 || reloaded.Workspace.Projects[0].Path != "apps/orders" {
		t.Fatalf("unexpected projects: %#v", reloaded.Workspace.Projects)
	}
}

// cargoRegistryFixture writes an offline Rust CLI registry with an independent shared root.
func cargoRegistryFixture(t *testing.T, root string) {
	t.Helper()
	manifest := core.NewManifest("cargo")
	manifest.Templates = []core.Template{{
		Name:    "rust-cli",
		Kind:    "app",
		Version: "0.1.0",
		Questions: []core.Question{{
			Prompt:   "CLI name:",
			Type:     "string",
			Populate: "name",
		}},
		Substitutions: map[string]string{"rust-cli": "name"},
	}}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "templates")
	selected := filepath.Join(shared, "rust-cli")
	writeFixtureFile(t, filepath.Join(shared, ".gitignore"), "# shared rust\n/target\n", 0o644)
	writeFixtureFile(t, filepath.Join(shared, ".gitattributes"), "*.rs text eol=lf\n", 0o644)
	writeFixtureFile(t, filepath.Join(shared, "NOTICE"), "shared Rust notice\n", 0o640)
	writeFixtureFile(t, filepath.Join(shared, "mise.toml"), "[tools]\nrust = '1.82'\n\n[tasks.shared]\nrun = 'echo shared-rust'\n\n[env]\nSHARED_ROOT = 'cargo'\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, ".gitignore"), "# rust cli\n*.profraw\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, ".gitattributes"), "Cargo.lock -diff\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, "NOTICE"), "selected Rust notice\n", 0o600)
	writeFixtureFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nrust = '1.83'\n\n[tasks.build]\nrun = 'cargo build'\n\n[env]\nAPP_KIND = 'rust-cli'\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, "Cargo.toml"), "[package]\nname = \"rust-cli\"\nversion = \"0.1.0\"\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, "src", "main.rs"), "fn main() { println!(\"rust-cli\"); }\n", 0o644)
}

// bunRegistryFixture writes an offline Hono registry whose selected Bun version requires one preflight update.
func bunRegistryFixture(t *testing.T, root string) {
	t.Helper()
	manifest := core.NewManifest("bun")
	manifest.Templates = []core.Template{{
		Name:    "hono-api",
		Kind:    "app",
		Version: "0.2.0",
		Questions: []core.Question{{
			Prompt:   "API name:",
			Type:     "string",
			Populate: "name",
		}},
		Substitutions: map[string]string{"hono-api": "name"},
	}}
	if err := core.SaveManifest(filepath.Join(root, core.ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "templates")
	selected := filepath.Join(shared, "hono-api")
	writeFixtureFile(t, filepath.Join(shared, ".gitignore"), "# shared bun\nnode_modules/\n", 0o644)
	writeFixtureFile(t, filepath.Join(shared, ".gitattributes"), "*.ts text eol=lf\n", 0o644)
	writeFixtureFile(t, filepath.Join(shared, "NOTICE"), "shared Bun notice\n", 0o640)
	writeFixtureFile(t, filepath.Join(shared, "mise.toml"), "[tools]\nbun = '1.2'\n\n[tasks.shared]\nrun = 'echo shared-bun'\n\n[env]\nSHARED_ROOT = 'bun'\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, ".gitignore"), "# hono api\n.env\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, ".gitattributes"), "bun.lock -diff\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, "NOTICE"), "selected Bun notice\n", 0o600)
	writeFixtureFile(t, filepath.Join(selected, "mise.toml"), "[tools]\nbun = '1.1'\n\n[tasks.build]\nrun = 'bun build src/index.ts'\n\n[env]\nAPP_KIND = 'hono-api'\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, "package.json"), "{\"name\":\"hono-api\",\"scripts\":{\"build\":\"bun build src/index.ts\"}}\n", 0o644)
	writeFixtureFile(t, filepath.Join(selected, "src", "index.ts"), "export const appName = 'hono-api';\n", 0o644)
}

func writeFixtureFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateFromCargoAndBunRegistries(t *testing.T) {
	log := installMiseTaskShim(t)
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if _, err := core.InitializeWorkspace(workspace, workspaceMiseTemplate(t)); err != nil {
		t.Fatal(err)
	}
	cargoRegistry := filepath.Join(base, "cargo-fixture")
	bunRegistry := filepath.Join(base, "bun-fixture")
	cargoRegistryFixture(t, cargoRegistry)
	bunRegistryFixture(t, bunRegistry)

	cargoSelector := cargoRegistry + ":rust-cli"
	cargoResolver := &fixedMergeResolver{Decisions: map[string]core.MergeDecision{
		"NOTICE": {Choice: core.MergeChoiceKeepShared},
	}}
	var output bytes.Buffer
	if err := core.Generate(context.Background(), workspace, cargoSelector, core.GenerateOptions{
		Questionnaire:    fixedQuestionnaire{Answers: map[string]string{"name": "orders-cli"}},
		ConflictResolver: cargoResolver.ResolveMergeConflict,
	}, &output); err != nil {
		t.Fatal(err)
	}

	bunSelector := bunRegistry + ":hono-api"
	bunResolver := &fixedMergeResolver{Decisions: map[string]core.MergeDecision{
		"NOTICE": {Choice: core.MergeChoiceUseSelected},
	}}
	if err := core.Generate(context.Background(), workspace, bunSelector, core.GenerateOptions{
		Questionnaire:    fixedQuestionnaire{Answers: map[string]string{"name": "gateway-api"}},
		ConflictResolver: bunResolver.ResolveMergeConflict,
	}, &output); err != nil {
		t.Fatal(err)
	}

	orders := filepath.Join(workspace, "apps", "orders-cli")
	gateway := filepath.Join(workspace, "apps", "gateway-api")
	assertFileContent(t, filepath.Join(orders, ".gitignore"), "# shared rust\n/target\n# rust cli\n*.profraw\n")
	assertFileContent(t, filepath.Join(orders, ".gitattributes"), "*.rs text eol=lf\nCargo.lock -diff\n")
	assertFileContent(t, filepath.Join(orders, "NOTICE"), "shared Rust notice\n")
	assertFileContains(t, filepath.Join(orders, "Cargo.toml"), "name = \"orders-cli\"")
	assertFileContains(t, filepath.Join(orders, "src", "main.rs"), "orders-cli")
	assertFileContent(t, filepath.Join(gateway, ".gitignore"), "# shared bun\nnode_modules/\n# hono api\n.env\n")
	assertFileContent(t, filepath.Join(gateway, ".gitattributes"), "*.ts text eol=lf\nbun.lock -diff\n")
	assertFileContent(t, filepath.Join(gateway, "NOTICE"), "selected Bun notice\n")
	assertFileContains(t, filepath.Join(gateway, "package.json"), "gateway-api")
	assertFileContains(t, filepath.Join(gateway, "src", "index.ts"), "gateway-api")
	assertMergedMise(t, filepath.Join(orders, "mise.toml"), "rust", "1.83", "cargo", "orders-cli")
	assertMergedMise(t, filepath.Join(gateway, "mise.toml"), "bun", "1.2", "bun", "gateway-api")

	manifest, err := core.LoadManifest(filepath.Join(workspace, core.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workspace.Projects) != 2 {
		t.Fatalf("projects = %#v", manifest.Workspace.Projects)
	}
	projects := make(map[string]core.Project, len(manifest.Workspace.Projects))
	for _, project := range manifest.Workspace.Projects {
		projects[project.Name] = project
	}
	if projects["orders-cli"].Template != cargoSelector || projects["gateway-api"].Template != bunSelector {
		t.Fatalf("project provenance = %#v", manifest.Workspace.Projects)
	}
	if _, err := os.Stat(filepath.Join(orders, "Cargo.toml")); err != nil {
		t.Fatalf("first destination changed after second generation: %v", err)
	}

	invocations := log.Invocations(t)
	contracts, err := core.ContractTasks("app")
	if err != nil {
		t.Fatal(err)
	}
	wantPerValidation := 1 + len(contracts)
	groups := map[string]int{}
	preflights := 0
	for _, invocation := range invocations {
		if invocation.Environment != "1" {
			t.Fatalf("PREMISE_TEMPLATE_TEST = %q for %#v", invocation.Environment, invocation)
		}
		if invocation.Directory == orders || invocation.Directory == gateway {
			t.Fatalf("validation ran in live destination: %#v", invocation)
		}
		groups[invocation.Directory]++
		if strings.Contains(invocation.Directory, "premise-tool-preflight-") {
			preflights++
		}
	}
	if len(groups) != 3 {
		t.Fatalf("validation directories = %#v, want Rust candidate, Bun preflight, Bun candidate", groups)
	}
	for directory, count := range groups {
		if count != wantPerValidation {
			t.Fatalf("Mise calls in %s = %d, want %d", directory, count, wantPerValidation)
		}
	}
	if preflights != wantPerValidation {
		t.Fatalf("preflight calls = %d, want exactly one %d-call preflight", preflights, wantPerValidation)
	}
	if strings.Count(output.String(), "[tool bun 1.1 -> 1.2] mise install") != 1 {
		t.Fatalf("Bun preflight output =\n%s", output.String())
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(content); got != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), want) {
		t.Fatalf("%s does not contain %q:\n%s", path, want, content)
	}
}

func assertMergedMise(t *testing.T, path, tool, version, sharedRoot, appKind string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := core.ExtractMiseConfig("generated", content)
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Tools[tool].Selector; got != version {
		t.Fatalf("%s tool %s = %q, want %q", path, tool, got, version)
	}
	if _, ok := config.Tasks["shared"]; !ok {
		t.Fatalf("%s missing shared task", path)
	}
	if _, ok := config.Tasks["build"]; !ok {
		t.Fatalf("%s missing selected build task", path)
	}
	if config.Env["SHARED_ROOT"] != sharedRoot || config.Env["APP_KIND"] != appKind {
		t.Fatalf("%s environment = %#v", path, config.Env)
	}
}

func TestGenerateValidationFailuresLeaveNoLiveState(t *testing.T) {
	for _, test := range []struct {
		name           string
		fixture        func(*testing.T, string)
		template       string
		project        string
		failDirectory  string
		failArgument   string
		failMise       string
		wantError      string
		mergeDecisions map[string]core.MergeDecision
	}{
		{
			name:           "selective tool update",
			fixture:        bunRegistryFixture,
			template:       "hono-api",
			project:        "broken-api",
			failMise:       "bun = '1.2'",
			wantError:      "tool update bun 1.1 -> 1.2 failed",
			mergeDecisions: map[string]core.MergeDecision{"NOTICE": {Choice: core.MergeChoiceUseSelected}},
		},
		{
			name:           "final candidate contract",
			fixture:        cargoRegistryFixture,
			template:       "rust-cli",
			project:        "broken-cli",
			failDirectory:  "premise-candidate-validation-",
			failArgument:   "run e2e",
			wantError:      "generated candidate validation failed",
			mergeDecisions: map[string]core.MergeDecision{"NOTICE": {Choice: core.MergeChoiceKeepShared}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			installMiseTaskShim(t)
			t.Setenv("MISE_FAIL_DIRECTORY", test.failDirectory)
			t.Setenv("MISE_FAIL_ARGUMENT", test.failArgument)
			t.Setenv("MISE_FAIL_MISE_CONTAINS", test.failMise)

			base := t.TempDir()
			workspace := filepath.Join(base, "workspace")
			manifestPath, err := core.InitializeWorkspace(workspace, workspaceMiseTemplate(t))
			if err != nil {
				t.Fatal(err)
			}
			registry := filepath.Join(base, "registry")
			test.fixture(t, registry)
			before, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			temporary := filepath.Join(base, "temporary")
			if err := os.MkdirAll(temporary, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", temporary)

			err = core.Generate(context.Background(), workspace, registry+":"+test.template, core.GenerateOptions{
				Questionnaire:    fixedQuestionnaire{Answers: map[string]string{"name": test.project}},
				ConflictResolver: (&fixedMergeResolver{Decisions: test.mergeDecisions}).ResolveMergeConflict,
			}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			if _, err := os.Stat(filepath.Join(workspace, "apps", test.project)); !os.IsNotExist(err) {
				t.Fatalf("destination exists after failure: %v", err)
			}
			after, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("manifest changed after failure:\n%s", after)
			}
			assertNoGenerationTemporaryDirectories(t, filepath.Join(workspace, "apps"), temporary)
		})
	}
}

func assertNoGenerationTemporaryDirectories(t *testing.T, roots ...string) {
	t.Helper()
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			for _, prefix := range []string{".premise-stage-", "premise-selected-", "premise-tool-preflight-", "premise-candidate-validation-"} {
				if strings.Contains(entry.Name(), prefix) {
					t.Fatalf("temporary directory leaked: %s", filepath.Join(root, entry.Name()))
				}
			}
		}
	}
}

func TestGenerateExplainsMissingTemplateManifest(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := core.InitializeWorkspace(workspace, workspaceMiseTemplate(t)); err != nil {
		t.Fatal(err)
	}

	templateSource := filepath.Join(t.TempDir(), "not-a-template")
	if err := os.MkdirAll(templateSource, 0o755); err != nil {
		t.Fatal(err)
	}
	err := core.Generate(context.Background(), workspace, templateSource+":premise-lib", fixedOptions(nil), io.Discard)
	if err == nil {
		t.Fatal("expected missing template manifest error")
	}
	want := "template source \"" + templateSource + "\" does not contain premise.yaml"
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "choose a Premise template repository or run `pm init` in that directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitializeWorkspacePreservesExistingMiseConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	const existing = "monorepo_root = true\n\n[tools]\nnode = \"lts\"\n"
	misePath := filepath.Join(root, "mise.toml")
	if err := os.WriteFile(misePath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := core.InitializeWorkspace(root, workspaceMiseTemplate(t)); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(misePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != existing {
		t.Fatalf("existing mise.toml changed:\n%s", content)
	}
}

func TestInitializeWorkspaceRejectsExistingMiseWithoutMonorepoMarker(t *testing.T) {
	root := filepath.Join(t.TempDir(), "example")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mise.toml"), []byte("[tools]\nnode = \"lts\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := core.InitializeWorkspace(root, workspaceMiseTemplate(t)); err == nil || !strings.Contains(err.Error(), "top-level monorepo_root = true") {
		t.Fatalf("expected monorepo marker error, got %v", err)
	}
}

func TestManifestRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), core.ManifestFilename)
	content := `workspace:
  name: example
  schema-version: "0.1"
  providers:
    ci: github
    tools: mise
    tasks: mise
    infra: pulumi
    versioning: svu
  projects: []
unknown: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadManifest(path); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("expected strict YAML error, got %v", err)
	}
}

func TestConfigRejectsDuplicateProjects(t *testing.T) {
	config := core.NewManifest("example")
	config.Workspace.Projects = []core.Project{
		{Name: "orders", Template: ".:app", Path: "apps/orders"},
		{Name: "orders", Template: ".:app", Path: "apps/orders-v2"},
	}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate project name") {
		t.Fatalf("expected duplicate project name error, got %v", err)
	}

	config.Workspace.Projects[1] = core.Project{Name: "payments", Template: ".:app", Path: "apps/orders"}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate project path") {
		t.Fatalf("expected duplicate project path error, got %v", err)
	}
}

func TestTemplateContractsCollectAllFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell fixture")
	}
	root := t.TempDir()
	for _, name := range []string{"app", "lib"} {
		if err := os.MkdirAll(filepath.Join(root, "templates", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mise"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	manifest := core.NewManifest("example")
	manifest.Templates = []core.Template{
		{Name: "app", Kind: "app", Questions: []core.Question{{Prompt: "App name:", Type: "string", Populate: "name"}}},
		{Name: "lib", Kind: "lib", Questions: []core.Question{{Prompt: "Library name:", Type: "string", Populate: "name"}}},
	}
	var output bytes.Buffer
	err := core.TestTemplateContracts(context.Background(), root, manifest, &output, io.Discard)
	if err == nil {
		t.Fatal("expected contract failures")
	}
	for _, expected := range []string{"template app task build failed", "template app task e2e failed", "template lib task publish:rc failed", "template lib task publish failed"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("combined error does not contain %q: %v", expected, err)
		}
	}
}
