package main

import (
	"net/url"

	"github.com/pulumi/pulumi-terraform-provider/sdks/go/neon/neon"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Private helpers ------------------------------------------------------------

// databaseResources is one project's branch, compute endpoint, role, and database.
type databaseResources struct {
	pulumi.ResourceState
	Branch *neon.Branch
	URL    pulumi.StringOutput
}

// databaseArgs identifies the existing project and branch to clone.
type databaseArgs struct {
	ProjectID string
	ParentID  string
	Stack     string
}

// database creates an isolated database using the published Neon provider.
func database(ctx *pulumi.Context, name string, args databaseArgs, opts ...pulumi.ResourceOption) (*databaseResources, error) {
	result := &databaseResources{}
	if err := ctx.RegisterComponentResource("client:neon:Database", name, result, opts...); err != nil {
		return nil, err
	}
	child := pulumi.Parent(result)
	branch, err := neon.NewBranch(ctx, name+"-branch", &neon.BranchArgs{
		ProjectId: pulumi.String(args.ProjectID),
		ParentId:  pulumi.String(args.ParentID),
		Name:      pulumi.String("premise-" + args.Stack + "-" + name),
	}, child)
	if err != nil {
		return nil, err
	}
	endpoint, err := neon.NewEndpoint(ctx, name+"-endpoint", &neon.EndpointArgs{
		ProjectId:             pulumi.String(args.ProjectID),
		BranchId:              branch.ID(),
		Type:                  pulumi.String("read_write"),
		AutoscalingLimitMinCu: pulumi.Float64(0.25),
		AutoscalingLimitMaxCu: pulumi.Float64(0.25),
	}, child)
	if err != nil {
		return nil, err
	}
	role, err := neon.NewRole(ctx, name+"-role", &neon.RoleArgs{
		ProjectId: pulumi.String(args.ProjectID),
		BranchId:  branch.ID(),
		Name:      pulumi.String("premise_app"),
	}, child, pulumi.DependsOn([]pulumi.Resource{endpoint}))
	if err != nil {
		return nil, err
	}
	database, err := neon.NewDatabase(ctx, name+"-database", &neon.DatabaseArgs{
		ProjectId: pulumi.String(args.ProjectID),
		BranchId:  branch.ID(),
		Name:      pulumi.String("premise_app"),
		OwnerName: role.Name,
	}, child)
	if err != nil {
		return nil, err
	}
	connection := pulumi.All(role.Name, role.Password, endpoint.Host, database.Name).ApplyT(func(v []interface{}) string {
		return (&url.URL{Scheme: "postgresql", User: url.UserPassword(v[0].(string), v[1].(string)), Host: v[2].(string), Path: "/" + v[3].(string), RawQuery: "sslmode=require"}).String()
	}).(pulumi.StringOutput)
	result.Branch = branch
	result.URL = pulumi.ToSecret(connection).(pulumi.StringOutput)
	if err := ctx.RegisterResourceOutputs(result, pulumi.Map{"url": result.URL, "branchId": branch.ID()}); err != nil {
		return nil, err
	}
	return result, nil
}
