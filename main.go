package main

import (
	_ "embed"

	"github.com/cloudvoyant/premise/cmd"
	core "github.com/cloudvoyant/premise/core"
)

// workspaceMiseTemplate is copied into each workspace initialized by pm.
//
//go:embed assets/workspace-mise.toml
var workspaceMiseTemplate string

//go:embed assets/github-workflows/on-commit.yml
var onCommitWorkflow string

//go:embed assets/github-workflows/on-deploy.yml
var onDeployWorkflow string

//go:embed assets/github-workflows/on-merge.yml
var onMergeWorkflow string

func main() {
	cmd.Execute(workspaceMiseTemplate,
		core.WorkflowAsset{Path: ".github/workflows/on-commit.yml", Content: onCommitWorkflow},
		core.WorkflowAsset{Path: ".github/workflows/on-deploy.yml", Content: onDeployWorkflow},
		core.WorkflowAsset{Path: ".github/workflows/on-merge.yml", Content: onMergeWorkflow},
	)
}
