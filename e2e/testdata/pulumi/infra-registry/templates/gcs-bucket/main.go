package main

import (
	"errors"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(runBucket)
}

func runBucket(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	gcp := config.New(ctx, "gcp")
	artifacts := map[string]string{}
	if err := cfg.TryObject("artifacts", &artifacts); err != nil && !errors.Is(err, config.ErrMissingVar) {
		return err
	}
	return defineBucket(ctx, gcp.Require("project"), cfg.Require("location"), artifacts)
}
