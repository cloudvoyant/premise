package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
)

const (
	updateInstallerURL    = "https://raw.githubusercontent.com/cloudvoyant/premise/main/install.sh"
	maximumInstallerBytes = 1 << 20
)

var updateCmd = &cobra.Command{
	Use:   "update [version]",
	Short: "Update the premise CLI",
	Long:  "Downloads the Premise installer and replaces the current CLI with the latest or requested release.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		version := ""
		if len(args) == 1 {
			var err error
			version, err = normalizeUpdateVersion(args[0])
			if err != nil {
				return err
			}
		}
		return updatePremise(cmd.Context(), version, updateInstallerURL, http.DefaultClient, os.Executable, cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

func normalizeUpdateVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	version := strings.TrimPrefix(value, "v")
	if _, err := semver.StrictNewVersion(version); err != nil {
		return "", fmt.Errorf("update version %q must be a semantic version such as v0.2.1: %w", value, err)
	}
	return "v" + version, nil
}

func updatePremise(
	ctx context.Context,
	version string,
	installerURL string,
	client *http.Client,
	executable func() (string, error),
	stdout io.Writer,
	stderr io.Writer,
) error {
	executablePath, err := executable()
	if err != nil {
		return fmt.Errorf("resolve current premise executable: %w", err)
	}
	executablePath, err = filepath.EvalSymlinks(executablePath)
	if err != nil {
		return fmt.Errorf("resolve current premise executable symlinks: %w", err)
	}
	installDirectory := filepath.Dir(executablePath)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, installerURL, nil)
	if err != nil {
		return fmt.Errorf("create installer request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download premise installer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download premise installer: %s", response.Status)
	}
	installer, err := io.ReadAll(io.LimitReader(response.Body, maximumInstallerBytes+1))
	if err != nil {
		return fmt.Errorf("read premise installer: %w", err)
	}
	if len(installer) > maximumInstallerBytes {
		return fmt.Errorf("read premise installer: response exceeds %d bytes", maximumInstallerBytes)
	}

	command := exec.CommandContext(ctx, "bash")
	command.Stdin = strings.NewReader(string(installer))
	command.Stdout = stdout
	command.Stderr = stderr
	command.Env = updateEnvironment(os.Environ(), installDirectory, version)
	if err := command.Run(); err != nil {
		return fmt.Errorf("run premise installer: %w", err)
	}
	return nil
}

func updateEnvironment(environment []string, installDirectory, version string) []string {
	filtered := make([]string, 0, len(environment)+2)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "INSTALL_DIR=") || strings.HasPrefix(entry, "VERSION=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "INSTALL_DIR="+installDirectory, "VERSION="+version)
}
