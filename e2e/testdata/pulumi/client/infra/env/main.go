package main

import (
	"fmt"
	"os"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		if os.Getenv("PREMISE_TEST_FAIL_INFRA") == "1" {
			return fmt.Errorf("requested infrastructure failure")
		}
		revision := os.Getenv("PREMISE_TEST_REVISION")
		if revision == "" {
			revision = "a"
		}
		bucket, err := storage.NewBucket(ctx, "storage-"+config.Require(ctx, "premise:environmentName"), &storage.BucketArgs{
			Versioning:               &storage.BucketVersioningArgs{Enabled: pulumi.Bool(true)},
			ForceDestroy:             pulumi.Bool(true),
			Project:                  pulumi.String(config.Require(ctx, "gcp:project")),
			Location:                 pulumi.String("US"),
			UniformBucketLevelAccess: pulumi.Bool(true),
			PublicAccessPrevention:   pulumi.String("enforced"),
			Labels:                   pulumi.StringMap{"premise-e2e": pulumi.String("true"), "revision": pulumi.String(revision)},
		})
		if err != nil {
			return err
		}
		ctx.Export("bucketName", bucket.Name)
		ctx.Export("bucketUrl", bucket.Url)
		return nil
	})
}
