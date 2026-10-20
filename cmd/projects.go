package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

// Public API -----------------------------------------------------------------

// ProjectsCommand creates the projects command.
func ProjectsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:  "projects",
		Args: cobra.NoArgs,
	}
	command.AddCommand(ProjectsListCommand())
	return command
}

// ProjectsListCommand creates the ls command.
func ProjectsListCommand() *cobra.Command {
	var flowName, projectType, platformMode, eligibility string
	var asJSON bool
	command := &cobra.Command{
		Use:  "ls",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := premiseRoot()
			if err != nil {
				return err
			}
			manifest, err := core.LoadManifest(root + "/" + core.ManifestFilename)
			if err != nil {
				return err
			}
			flow := core.CIFlow(flowName)
			if flow == "" {
				flow = core.CIFlowOnCommit
			}
			projects, err := core.SelectCIProjects(root, manifest, flow)
			if err != nil {
				return err
			}
			if projectType != "" || platformMode != "" || eligibility != "" {
				filtered := projects[:0]
				for _, p := range projects {
					if projectType != "" && p.Kind != projectType {
						continue
					}
					if platformMode == "single" && len(p.Platforms()) != 1 {
						continue
					}
					if platformMode == "multi" && len(p.Platforms()) <= 1 {
						continue
					}
					if eligibility == "eligible" && !p.Eligible {
						continue
					}
					if eligibility == "excluded" && p.Eligible {
						continue
					}
					filtered = append(filtered, p)
				}
				projects = filtered
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(projects)
			}
			for _, p := range projects {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Path, p.Kind, p.Source, strings.Join(p.Platforms(), ","))
			}
			return nil
		},
	}
	command.Flags().StringVar(&projectType, "type", "", "filter by app or lib")
	command.Flags().StringVar(&flowName, "flow", "on-commit", "CI flow")
	command.Flags().StringVar(&platformMode, "platform-mode", "", "filter by single or multi platform")
	command.Flags().StringVar(&eligibility, "eligibility", "", "filter by eligible, excluded, or all")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return command
}
