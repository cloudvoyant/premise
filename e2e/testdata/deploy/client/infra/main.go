package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi-postgresql/sdk/v3/go/postgresql"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Private helpers ------------------------------------------------------------

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "postgresql")
		host, port := cfg.Require("host"), cfg.RequireInt("port")
		artifacts := map[string]string{}
		if err := json.Unmarshal([]byte(os.Getenv("PM_ARTIFACTS")), &artifacts); err != nil {
			return err
		}
		for _, app := range []string{"api", "worker", "website"} {
			if artifacts[app] == "" {
				continue
			}
			dependencies := []pulumi.Resource{}
			if app != "website" {
				database, err := postgresql.NewDatabase(ctx, app+"-database", &postgresql.DatabaseArgs{})
				if err != nil {
					return err
				}
				connection := pulumi.Sprintf("postgresql://postgres@%s:%d/%s?sslmode=disable", host, port, database.Name)
				ctx.Export(app+"DatabaseUrl", pulumi.ToSecret(connection))
				dependencies = append(dependencies, database)
			}
			update := `cp "$SOURCE" "$DESTINATION.tmp" && mv "$DESTINATION.tmp" "$DESTINATION"`
			if os.Getenv("FAIL_UPDATE_APP") == app && os.Getenv("PM_RELEASE") == "B" {
				update = `attempt=0; while [ "$attempt" -lt 100 ]; do if [ "$(cat "$PEER_RELEASE")" = B ]; then touch "$UPDATE_OBSERVED"; echo "injected application update failure" >&2; exit 1; fi; attempt=$((attempt + 1)); sleep 0.1; done; echo "peer application never updated" >&2; exit 1`
			}
			_, err := local.NewCommand(ctx, app+"-deployment", &local.CommandArgs{
				Create: pulumi.String(update),
				Update: pulumi.String(update),
				Environment: pulumi.StringMap{
					"SOURCE":          pulumi.String(artifacts[app]),
					"DESTINATION":     pulumi.String(filepath.Join(os.Getenv("APP_STATE"), app+"-release")),
					"PEER_RELEASE":    pulumi.String(filepath.Join(os.Getenv("APP_STATE"), "api-release")),
					"UPDATE_OBSERVED": pulumi.String(filepath.Join(os.Getenv("APP_STATE"), "partial-update-observed")),
				},
				Triggers: pulumi.Array{pulumi.String(os.Getenv("PM_RELEASE"))},
			}, pulumi.DependsOn(dependencies))
			if err != nil {
				return err
			}
		}
		return nil
	})
}
