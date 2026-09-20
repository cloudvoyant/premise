package core

import (
	"strings"
	"testing"
)

func TestExtractMiseConfigNormalizesTasksAndPreservesUnknownRoot(t *testing.T) {
	config, err := ExtractMiseConfig("selected", []byte(`min_version = "2026.1"
unknown = { enabled = true }

[tools]
go = "1.25"

[tasks]
build = "go build ./..."
test = { run = ["go test ./...", "echo done"], description = "test" }

[env]
MODE = "test"
`))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Tools["go"].StringSelector || config.Tools["go"].Selector != "1.25" {
		t.Fatalf("unexpected tool: %#v", config.Tools["go"])
	}
	if !config.Tasks["build"].Shorthand || len(config.Tasks["build"].Run) != 1 {
		t.Fatalf("unexpected shorthand task: %#v", config.Tasks["build"])
	}
	if len(config.Tasks["test"].Run) != 2 || config.Env["MODE"] != "test" {
		t.Fatalf("unexpected typed views: tasks=%#v env=%#v", config.Tasks, config.Env)
	}
	encoded, err := encodeMiseConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "[unknown]") || !strings.Contains(string(encoded), "[tasks.build]") {
		t.Fatalf("canonical output lost normalized or unknown values:\n%s", encoded)
	}
}

func TestExtractMiseConfigRejectsMalformedSectionsIndependently(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "tools", data: `tools = "go"`, want: "shared mise.toml section tools must be a table"},
		{name: "tasks", data: `tasks = ["build"]`, want: "shared mise.toml section tasks must be a table"},
		{name: "env", data: `env = "test"`, want: "shared mise.toml section env must be a table"},
		{name: "task run", data: "[tasks.build]\nrun = 1\n", want: "shared mise.toml task build run must be a string or string list"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ExtractMiseConfig("shared", []byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
