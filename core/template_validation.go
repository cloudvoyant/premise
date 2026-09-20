package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ValidateToolChanges(ctx context.Context, selectedStage, kind string, changes []ToolChange, output io.Writer) error {
	for _, change := range changes {
		candidate, err := os.MkdirTemp("", "premise-tool-preflight-*")
		if err != nil {
			return fmt.Errorf("create %s tool preflight: %w", change.Name, err)
		}
		failure := func() error {
			defer os.RemoveAll(candidate)
			if err := copyTree(selectedStage, candidate, nil); err != nil {
				return err
			}
			path := filepath.Join(candidate, "mise.toml")
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read selected mise.toml: %w", err)
			}
			config, err := ExtractMiseConfig("selected preflight", data)
			if err != nil {
				return err
			}
			tools, ok := config.Root["tools"].(map[string]any)
			if !ok {
				return fmt.Errorf("selected mise.toml tools section disappeared during %s preflight", change.Name)
			}
			if _, ok := tools[change.Name]; !ok {
				return fmt.Errorf("selected mise.toml tool %s disappeared during preflight", change.Name)
			}
			tools[change.Name] = cloneAny(change.To)
			config, err = extractMiseRoot("selected preflight", config.Root)
			if err != nil {
				return err
			}
			encoded, err := encodeMiseConfig(config)
			if err != nil {
				return err
			}
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("inspect selected mise.toml: %w", err)
			}
			if err := os.WriteFile(path, encoded, info.Mode().Perm()); err != nil {
				return fmt.Errorf("write selected mise.toml: %w", err)
			}
			label := fmt.Sprintf("tool %s %v -> %v", change.Name, change.From, change.To)
			if err := runTemplateContracts(ctx, candidate, kind, label, output, output); err != nil {
				return fmt.Errorf("tool update %s %v -> %v failed: %w", change.Name, change.From, change.To, err)
			}
			return nil
		}()
		if failure != nil {
			return failure
		}
	}
	return nil
}

func ValidateGeneratedCandidate(ctx context.Context, stage, kind string, output io.Writer) error {
	candidate, err := os.MkdirTemp("", "premise-candidate-validation-*")
	if err != nil {
		return fmt.Errorf("create generated candidate validation copy: %w", err)
	}
	defer os.RemoveAll(candidate)
	if err := copyTree(stage, candidate, nil); err != nil {
		return fmt.Errorf("copy generated candidate: %w", err)
	}
	if err := runTemplateContracts(ctx, candidate, kind, "merged candidate", output, output); err != nil {
		return fmt.Errorf("generated candidate validation failed: %w", err)
	}
	return nil
}
