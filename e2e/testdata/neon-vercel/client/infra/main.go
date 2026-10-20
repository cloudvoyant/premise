package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "client")
		api, err := database(ctx, "api", databaseArgs{
			ProjectID: cfg.Require("neonProjectId"),
			ParentID:  cfg.Require("neonParentId"),
			Stack:     ctx.Stack(),
		})
		if err != nil {
			return err
		}
		worker := api
		if cfg.Get("scenario") != "shared" {
			worker, err = database(ctx, "worker", databaseArgs{
				ProjectID: cfg.Require("neonProjectId"),
				ParentID:  cfg.Require("neonParentId"),
				Stack:     ctx.Stack(),
			})
			if err != nil {
				return err
			}
		}
		ctx.Export("apiDatabaseUrl", api.URL)
		ctx.Export("workerDatabaseUrl", worker.URL)
		artifacts := map[string]string{}
		if err := json.Unmarshal([]byte(os.Getenv("PM_ARTIFACTS")), &artifacts); err != nil {
			return fmt.Errorf("expected PM_ARTIFACTS with a retained web source directory: %w", err)
		}
		if artifacts["web"] == "" {
			return fmt.Errorf("web artifact reference is required")
		}
		projectID := cfg.Require("vercelProjectId")
		if cfg.Get("scenario") == "fail-apply" && strings.HasSuffix(os.Getenv("PM_RELEASE"), "-B") {
			projectID = "prj_premise_missing_test_project"
		}
		web, err := application(ctx, "web", applicationArgs{
			ProjectID:   projectID,
			TeamID:      cfg.Require("vercelTeamId"),
			Alias:       fmt.Sprintf("%s-%s.vercel.app", cfg.Require("aliasPrefix"), ctx.Stack()),
			Source:      artifacts["web"],
			Release:     os.Getenv("PM_RELEASE"),
			Environment: pulumi.StringMap{"API_DATABASE_URL": api.URL, "WORKER_DATABASE_URL": worker.URL},
		})
		if err != nil {
			return err
		}
		ctx.Export("deploymentId", web.Deployment.ID())
		ctx.Export("applicationUrl", pulumi.Sprintf("https://%s", web.Alias.Alias))
		return nil
	})
}
