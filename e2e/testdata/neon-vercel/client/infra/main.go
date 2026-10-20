package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "client")
		db, err := database(ctx, "web", databaseArgs{
			ProjectID: cfg.Require("neonProjectId"),
			ParentID:  cfg.Require("neonParentId"),
			Stack:     ctx.Stack(),
		})
		if err != nil {
			return err
		}
		ctx.Export("databaseUrl", db.URL)
		artifacts := map[string]string{}
		if err := json.Unmarshal([]byte(os.Getenv("PM_ARTIFACTS")), &artifacts); err != nil {
			return fmt.Errorf("expected PM_ARTIFACTS with a retained web source directory: %w", err)
		}
		if artifacts["web"] == "" {
			return fmt.Errorf("web artifact reference is required")
		}
		web, err := application(ctx, "web", applicationArgs{
			ProjectID:   cfg.Require("vercelProjectId"),
			TeamID:      cfg.Require("vercelTeamId"),
			Alias:       fmt.Sprintf("%s-%s.vercel.app", cfg.Require("aliasPrefix"), ctx.Stack()),
			Source:      artifacts["web"],
			Release:     os.Getenv("PM_RELEASE"),
			Environment: pulumi.StringMap{"DATABASE_URL": db.URL},
		})
		if err != nil {
			return err
		}
		ctx.Export("deploymentId", web.Deployment.ID())
		ctx.Export("applicationUrl", pulumi.Sprintf("https://%s", web.Alias.Alias))
		return nil
	})
}
