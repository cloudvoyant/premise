module example.com/environment

go 1.25.6

require (
 github.com/pulumi/pulumi-gcp/sdk/v9 v9.0.0
 github.com/pulumi/pulumi/sdk/v3 v3.225.0
 example.com/database v0.0.0
 example.com/web-deployment v0.0.0
 example.com/admin-deployment v0.0.0
 example.com/client-identity v0.0.0
)
replace example.com/database => ../components/database
replace example.com/web-deployment => ../components/web-deployment
replace example.com/admin-deployment => ../components/admin-deployment
replace example.com/client-identity => ../components/client
replace github.com/pulumi/pulumi-terraform-provider/sdks/go/neon => ../components/database/sdks/neon/go
