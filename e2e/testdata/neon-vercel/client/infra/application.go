package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumiverse/pulumi-vercel/sdk/v5/go/vercel"
)

// Private helpers ------------------------------------------------------------

// applicationResources deploys one client project and gives it a stable environment alias.
type applicationResources struct {
	pulumi.ResourceState
	Deployment *vercel.Deployment
	Alias      *vercel.Alias
}

// applicationArgs carries project-owned source and runtime inputs.
type applicationArgs struct {
	ProjectID   string
	TeamID      string
	Alias       string
	Source      string
	Release     string
	Environment pulumi.StringMap
}

// application deploys an application using the published Vercel provider.
func application(ctx *pulumi.Context, name string, args applicationArgs, opts ...pulumi.ResourceOption) (*applicationResources, error) {
	application := &applicationResources{}
	if err := ctx.RegisterComponentResource("client:vercel:Application", name, application, opts...); err != nil {
		return nil, err
	}
	source, err := relativeSource(args.Source)
	if err != nil {
		return nil, err
	}
	directory, err := vercel.GetProjectDirectory(ctx, &vercel.GetProjectDirectoryArgs{Path: source}, pulumi.Parent(application))
	if err != nil {
		return nil, err
	}
	if err := validateFiles(directory.Files); err != nil {
		return nil, err
	}
	environment := pulumi.StringMap{}
	for key, value := range args.Environment {
		environment[key] = value
	}
	environment["PREMISE_RELEASE"] = pulumi.String(args.Release)
	deployment, err := vercel.NewDeployment(ctx, name+"-deployment", &vercel.DeploymentArgs{
		ProjectId:       pulumi.String(args.ProjectID),
		TeamId:          pulumi.String(args.TeamID),
		Files:           pulumi.ToStringMap(directory.Files),
		PathPrefix:      pulumi.String(directory.Path),
		Production:      pulumi.Bool(false),
		DeleteOnDestroy: pulumi.Bool(true),
		Environment:     pulumi.ToSecret(environment).(pulumi.StringMapOutput),
		Meta: pulumi.StringMap{
			"premiseRelease": pulumi.String(args.Release),
			"premiseStack":   pulumi.String(ctx.Stack()),
		},
		ProjectSettings: &vercel.DeploymentProjectSettingsArgs{
			Framework:      pulumi.String("tanstack-start"),
			BuildCommand:   pulumi.String("bun run build"),
			InstallCommand: pulumi.String("bun install"),
		},
	}, pulumi.Parent(application), pulumi.RetainOnDelete(true))
	if err != nil {
		return nil, err
	}
	alias, err := vercel.NewAlias(ctx, name+"-alias", &vercel.AliasArgs{
		TeamId:       pulumi.String(args.TeamID),
		Alias:        pulumi.String(args.Alias),
		DeploymentId: deployment.ID(),
	}, pulumi.Parent(application))
	if err != nil {
		return nil, err
	}
	application.Deployment, application.Alias = deployment, alias
	if err := ctx.RegisterResourceOutputs(application, pulumi.Map{"deploymentId": deployment.ID(), "alias": alias.Alias}); err != nil {
		return nil, err
	}
	return application, nil
}

func relativeSource(source string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// Artifact references are workspace-relative so records survive a new runner.
	root := filepath.Clean(filepath.Join(cwd, ".."))
	if !filepath.IsAbs(source) {
		source = filepath.Join(root, source)
	}
	absolute, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("application source must stay inside workspace")
	}
	return filepath.Rel(cwd, absolute)
}

func validateFiles(files map[string]string) error {
	if len(files) == 0 {
		return fmt.Errorf("application source has no deployable files; check .vercelignore")
	}
	for file := range files {
		for _, part := range strings.Split(filepath.ToSlash(file), "/") {
			if strings.HasPrefix(part, ".env") {
				return fmt.Errorf("source contains secret file; fix .vercelignore")
			}
			switch part {
			case ".pulumi", ".pulumi-state", ".premise", "node_modules", "test-results", ".git", ".npmrc", "fnox.toml":
				return fmt.Errorf("source contains secret/state/dependency file; fix .vercelignore")
			}
		}
	}
	return nil
}
