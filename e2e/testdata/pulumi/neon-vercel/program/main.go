package main

import (
	admin "example.com/admin-deployment"
	client "example.com/client-identity"
	database "example.com/database"
	web "example.com/web-deployment"
	"fmt"
	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/secretmanager"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"os"
)

// Private helpers ------------------------------------------------------------

func main() { pulumi.Run(deploy) }

func deploy(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	identity, err := client.New(ctx, ctx.Stack())
	if err != nil {
		return err
	}
	stack := identity.Label
	artifacts := map[string]string{}
	cfg.RequireObject("artifacts", &artifacts)
	db, err := database.New(ctx, "db", database.Args{ProjectID: cfg.Require("neonProjectId"), ParentID: cfg.Require("neonParentBranchId"), Stack: stack})
	if err != nil {
		return err
	}
	// Publish this preview's app values through a real Secret Manager resource.
	// The migration's fnox profile reads these only after the prerequisite update.
	for suffix, value := range map[string]pulumi.StringInput{"database-url": db.URL, "app-marker": pulumi.String(os.Getenv("APP_MARKER"))} {
		secret, err := secretmanager.NewSecret(ctx, "db-secret-"+suffix, &secretmanager.SecretArgs{Project: pulumi.String(cfg.Require("gcpProject")), SecretId: pulumi.String("premise-e2e-" + stack + "-" + suffix), Replication: &secretmanager.SecretReplicationArgs{Auto: &secretmanager.SecretReplicationAutoArgs{}}})
		if err != nil {
			return err
		}
		if _, err := secretmanager.NewSecretVersion(ctx, "db-secret-version-"+suffix, &secretmanager.SecretVersionArgs{Secret: secret.ID(), SecretData: pulumi.ToSecret(value).(pulumi.StringOutput)}); err != nil {
			return err
		}
	}
	alias := fmt.Sprintf("premise-%s-%s.vercel.app", stack[len(stack)-10:], cfg.Require("vercelTeamSlug"))
	application, err := web.New(ctx, "app", web.Args{ProjectID: cfg.Require("vercelProjectId"), TeamID: cfg.Require("vercelTeamId"), Alias: alias, Source: artifacts["web"], Release: os.Getenv("PM_RELEASE"), Environment: pulumi.StringMap{"DATABASE_URL": db.URL, "APP_MARKER": pulumi.String(os.Getenv("APP_MARKER"))}})
	if err != nil {
		return err
	}
	// A second rendering of the Vercel template is imported independently and
	// instantiated in the same stack. It receives its own source binding and alias.
	adminApp, err := admin.New(ctx, "admin", admin.Args{ProjectID: cfg.Require("vercelProjectId"), TeamID: cfg.Require("vercelTeamId"), Alias: "admin-" + alias, Source: artifacts["admin"], Release: os.Getenv("PM_RELEASE"), Environment: pulumi.StringMap{"DATABASE_URL": db.URL, "APP_MARKER": pulumi.String(os.Getenv("APP_MARKER"))}})
	if err != nil {
		return err
	}
	ctx.Export("databaseURL", db.URL)
	ctx.Export("databaseBranchId", db.Branch.ID())
	ctx.Export("deploymentId", application.Deployment.ID())
	ctx.Export("appURL", pulumi.Sprintf("https://%s", application.Alias.Alias))
	ctx.Export("aliasName", application.Alias.Alias)
	ctx.Export("adminDeploymentId", adminApp.Deployment.ID())
	ctx.Export("adminAliasName", adminApp.Alias.Alias)
	ctx.Export("adminURL", pulumi.Sprintf("https://%s", adminApp.Alias.Alias))
	ctx.Export("clientLabel", pulumi.String(stack))
	return nil
}
