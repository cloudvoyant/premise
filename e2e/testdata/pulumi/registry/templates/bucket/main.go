package main

import (
	"os"
	"path/filepath"

	"github.com/pulumi/pulumi-gcp/sdk/v9/go/gcp/storage"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		bucket, err := storage.NewBucket(ctx, "premise-e2e", &storage.BucketArgs{
			Project:                  pulumi.String(os.Getenv("GCP_PROJECT_ID")),
			Location:                 pulumi.String("US"),
			UniformBucketLevelAccess: pulumi.Bool(true),
			PublicAccessPrevention:   pulumi.String("enforced"),
			Labels:                   pulumi.StringMap{"premise-e2e": pulumi.String("true")},
		})
		if err != nil {
			return err
		}
		artifacts := map[string]string{}
		config.New(ctx, "").RequireObject("artifacts", &artifacts)
		object, err := storage.NewBucketObject(ctx, "dummy", &storage.BucketObjectArgs{
			Bucket: bucket.Name,
			Name:   pulumi.String("hello.txt"),
			Source: pulumi.NewFileAsset(filepath.Join(artifacts["dummy"], "hello.txt")),
		})
		if err != nil {
			return err
		}
		ctx.Export("bucketName", bucket.Name)
		ctx.Export("objectName", object.Name)
		return nil
	})
}
