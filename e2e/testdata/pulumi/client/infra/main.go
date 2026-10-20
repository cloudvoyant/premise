package main

import (
	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws/s3"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		bucket, err := s3.NewBucket(ctx, "storage", &s3.BucketArgs{
			ForceDestroy: pulumi.Bool(false),
			Tags:         pulumi.StringMap{"premise-e2e": pulumi.String("true"), "environment": pulumi.String(ctx.Stack())},
		})
		if err != nil {
			return err
		}
		object, err := s3.NewBucketObjectv2(ctx, "dummy", &s3.BucketObjectv2Args{
			Bucket: bucket.ID(),
			Key:    pulumi.String("hello.txt"),
			Source: pulumi.NewFileAsset("../apps/dummy/artifact/hello.txt"),
		})
		if err != nil {
			return err
		}
		ctx.Export("bucketName", bucket.Bucket)
		ctx.Export("bucketArn", bucket.Arn)
		ctx.Export("objectName", object.Key)
		return nil
	})
}
