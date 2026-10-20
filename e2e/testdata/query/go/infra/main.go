package main

import (
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		id, err := random.NewRandomId(ctx, "duplicate", &random.RandomIdArgs{ByteLength: pulumi.Int(8)})
		if err != nil {
			return err
		}
		if _, err := random.NewRandomString(ctx, "duplicate", &random.RandomStringArgs{Length: pulumi.Int(8)}); err != nil {
			return err
		}
		ctx.Export("publicId", id.Hex)
		ctx.Export("branchMarker", pulumi.String(ctx.Stack()))
		ctx.Export("privateValue", pulumi.ToSecret(pulumi.String("query-fixture-secret")))
		return nil
	})
}
