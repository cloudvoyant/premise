package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Fnox resolves local application values with an explicitly sanitized environment.
type Fnox struct{ Environment []string }

// Public API -----------------------------------------------------------------

// Read resolves scalar values without forwarding provider diagnostics.
func (reader Fnox) Read(ctx context.Context, directory, config, profile string, keys []string) (map[string]string, error) {
	command := exec.CommandContext(ctx, "mise", "exec", "fnox@1.39.0", "--", "fnox", "--config", config, "--profile", profile, "--no-defaults", "--no-daemon", "--non-interactive", "--if-missing", "error", "env", "--json", "--keys", strings.Join(keys, ","))
	command.Dir = directory
	// Avoid accidental profile/config overrides and verbose provider logs.
	for _, entry := range reader.Environment {
		if !strings.HasPrefix(entry, "FNOX_") && !strings.HasPrefix(entry, "RUST_LOG=") {
			command.Env = append(command.Env, entry)
		}
	}
	var output bytes.Buffer
	command.Stdout = &output
	// Provider diagnostics can contain credentials; report the key, never their text.
	if err := command.Run(); err != nil {
		return nil, errors.New("resolve application secrets: fnox failed")
	}
	var result struct {
		Schema  int               `json:"schema"`
		Set     map[string]string `json:"set"`
		Files   map[string]string `json:"files"`
		Remove  []string          `json:"remove"`
		Missing []string          `json:"missing"`
		Leases  []string          `json:"leases"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Schema != 1 {
		return nil, errors.New("invalid fnox environment response")
	}
	if len(result.Files) > 0 || len(result.Leases) > 0 || len(result.Missing) > 0 {
		return nil, errors.New("pm secrets requires resolved scalar secrets; use fnox exec for files or leases")
	}
	// This adapter returns scalar values, not a complete process environment. Refuse
	// inherited variables that fnox would remove rather than silently forwarding them.
	for _, key := range result.Remove {
		if os.Getenv(key) != "" && !AdministrationSecretKeys()[key] && !strings.HasPrefix(key, "FNOX_") && key != "RUST_LOG" {
			return nil, fmt.Errorf("fnox requires removing inherited %s; unset it or use fnox exec", key)
		}
	}
	return result.Set, nil
}
