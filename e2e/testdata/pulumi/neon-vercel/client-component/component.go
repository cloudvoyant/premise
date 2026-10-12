package client

import "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

// ProjectIdentity is a component authored by this client, without a registry template.
type ProjectIdentity struct {
	pulumi.ResourceState
	Label string
}

// Public API -----------------------------------------------------------------

// New groups the environment identity with the project's resources.
func New(ctx *pulumi.Context, stack string) (*ProjectIdentity, error) {
	identity := &ProjectIdentity{Label: stack}
	if err := ctx.RegisterComponentResource("client:identity:Project", "client-identity", identity); err != nil {
		return nil, err
	}
	return identity, ctx.RegisterResourceOutputs(identity, pulumi.Map{"label": pulumi.String(stack)})
}
