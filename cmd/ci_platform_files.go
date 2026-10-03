package cmd

import (
	"os"

	core "github.com/cloudvoyant/premise/core"
	"github.com/spf13/cobra"
)

var (
	platformFilesSource   string
	platformFilesSuffixes string
)

var ciPlatformFilesCmd = &cobra.Command{
	Use:   "platform-files <prepare|build|publish|publish:rc>",
	Short: "Prepare a version override or collect platform release files",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return core.CollectPlatformFiles(cwd, args[0], platformFilesSource, platformFilesSuffixes)
	},
}

var ciPlatformConfigCmd = &cobra.Command{
	Use:   "platform-config <build|publish|publish:rc>",
	Short: "Write a release-version override for a native build",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return core.PreparePlatformConfig(cwd, args[0])
	},
}

func init() {
	ciPlatformFilesCmd.Flags().StringVar(&platformFilesSource, "source", "", "project-relative build output directory")
	ciPlatformFilesCmd.Flags().StringVar(&platformFilesSuffixes, "suffixes", "", "comma-separated file suffixes")
	ciCmd.AddCommand(ciPlatformFilesCmd, ciPlatformConfigCmd)
}
