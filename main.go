package main

import (
	_ "embed"

	"github.com/cloudvoyant/premise/cmd"
)

// workspaceMiseTemplate is copied into each workspace initialized by pm.
//
//go:embed assets/workspace-mise.toml
var workspaceMiseTemplate string

func main() {
	cmd.Execute(workspaceMiseTemplate)
}
