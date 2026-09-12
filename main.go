package main

import (
	_ "embed"

	"github.com/cloudvoyant/premise/cmd"
)

// version is injected at build time via -ldflags "-X main.version=$VERSION".
var version = "dev"

// workspaceMiseTemplate is copied into each workspace initialized by pm.
//
//go:embed templates/mise.toml
var workspaceMiseTemplate string

func main() {
	cmd.Execute(version, workspaceMiseTemplate)
}
