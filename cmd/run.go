package cmd

import (
	"errors"
	"fmt"
	"strings"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run <task|project:task> [args...]",
	Short: "Run a workspace or generated-project task",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := premiseRoot()
		if err != nil {
			return err
		}
		selector, taskArguments := args[0], args[1:]
		if project, task, isProjectTask := strings.Cut(selector, ":"); isProjectTask {
			if project == "" || task == "" {
				return fmt.Errorf("invalid project task %q: expected project:task", selector)
			}
			err := core.RunProjectTask(cmd.Context(), root, project, task, cmd.OutOrStdout(), cmd.ErrOrStderr(), taskArguments...)
			if !errors.Is(err, core.ErrProjectNotFound) {
				return err
			}
		}
		return core.RunRootTask(cmd.Context(), root, selector, cmd.OutOrStdout(), cmd.ErrOrStderr(), taskArguments...)
	},
}
