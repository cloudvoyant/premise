package core

import (
	"strings"
	"testing"
)

func TestWithoutEnvironment(t *testing.T) {
	environment := []string{"PATH=/bin", "GITHUB_TOKEN=github", "CARGO_REGISTRY_TOKEN=cargo", "VALUE=a=b"}
	filtered := withoutEnvironment(environment, "GITHUB_TOKEN", "CARGO_REGISTRY_TOKEN")
	if got := strings.Join(filtered, "\n"); got != "PATH=/bin\nVALUE=a=b" {
		t.Fatalf("WithoutEnvironment() = %q", got)
	}
}

func TestRunnerEnvironmentReplacesManagedValues(t *testing.T) {
	t.Setenv("MISE_CEILING_PATHS", "/old")
	t.Setenv("PREMISE_TEMPLATE_TEST", "0")
	for _, name := range publicationCredentialEnvironment {
		t.Setenv(name, "secret")
	}
	runner := miseRunner{Ceiling: "/workspace"}
	environment := runner.environment([]string{"PREMISE_TEMPLATE_TEST=1"})
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, want := range []string{"\nMISE_CEILING_PATHS=/workspace\n", "\nPREMISE_TEMPLATE_TEST=1\n"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Environment() omitted %q", want)
		}
	}
	for _, unwanted := range []string{
		"\nMISE_CEILING_PATHS=/old\n",
		"\nPREMISE_TEMPLATE_TEST=0\n",
		"\nGITHUB_TOKEN=secret\n",
		"\nGH_TOKEN=secret\n",
		"\nCARGO_REGISTRY_TOKEN=secret\n",
		"\nCARGO_TOKEN=secret\n",
		"\nCRATES_TOKEN=secret\n",
	} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("Environment() retained %q", unwanted)
		}
	}
}
