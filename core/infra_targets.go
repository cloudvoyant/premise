package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Private helpers ------------------------------------------------------------

// The wire format is deliberately small: core depends on Pulumi's CLI plan,
// not its provider SDK or automation engine.
type infraPlanGoal struct {
	Type                 string              `json:"type"`
	Custom               bool                `json:"custom"`
	Parent               string              `json:"parent"`
	Dependencies         []string            `json:"dependencies"`
	PropertyDependencies map[string][]string `json:"propertyDependencies"`
	Provider             string              `json:"provider"`
}
type infraResourcePlan struct {
	Goal  *infraPlanGoal `json:"goal"`
	Steps []string       `json:"steps"`
}

func resolvePrerequisiteTargets(ctx context.Context, r infraRunner, d InfraResolution, patterns []string, env map[string]string) ([]string, error) {
	scopedEnv := map[string]string{}
	for k, v := range env {
		scopedEnv[k] = v
	}
	scopedEnv["PULUMI_BACKEND_URL"] = d.BackendURL
	env = scopedEnv
	directory, err := os.MkdirTemp("", "premise-preview-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "plan.json")
	if err := r.run(ctx, d.Directory, env, "pulumi", "preview", "--stack", d.Stack, "--non-interactive", "--save-plan", path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prerequisite preview: %w", err)
	}
	var versioned struct {
		Version int `json:"version"`
		Plan    struct {
			Resources map[string]infraResourcePlan `json:"resourcePlans"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(data, &versioned); err != nil {
		return nil, fmt.Errorf("decode prerequisite preview: %w", err)
	}
	if versioned.Version == 0 {
		var direct struct {
			Resources map[string]infraResourcePlan `json:"resourcePlans"`
		}
		if err := json.Unmarshal(data, &direct); err != nil {
			return nil, err
		}
		if direct.Resources != nil {
			return validatePrerequisiteCoverage(direct.Resources, patterns, d.Stack, d.Program.Name)
		}
	}
	if versioned.Version != 1 {
		return nil, fmt.Errorf("unsupported Pulumi plan version %d", versioned.Version)
	}
	return validatePrerequisiteCoverage(versioned.Plan.Resources, patterns, d.Stack, d.Program.Name)
}
func validatePrerequisiteCoverage(resources map[string]infraResourcePlan, patterns []string, stack, project string) ([]string, error) {
	selected := map[string]bool{}
	for _, pattern := range patterns {
		pattern = strings.ReplaceAll(strings.ReplaceAll(pattern, "{stack}", stack), "{project}", project)
		matched := false
		for urn, resource := range resources {
			ok, err := filepath.Match(pattern, urn)
			if err != nil {
				return nil, fmt.Errorf("invalid prerequisite pattern %q", pattern)
			}
			if ok {
				if resource.Goal == nil {
					return nil, fmt.Errorf("prerequisite %s is being deleted", urn)
				}
				selected[urn] = true
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("prerequisite pattern %q matches no resource", pattern)
		}
	}
	// Include component parents and providers needed to register the selected resources.
	changed := true
	for changed {
		changed = false
		for urn := range selected {
			goal := resources[urn].Goal
			if goal == nil {
				continue
			}
			for _, dependency := range []string{goal.Parent, providerURN(goal.Provider)} {
				if dependency == "" || selected[dependency] {
					continue
				}
				resource, ok := resources[dependency]
				if !ok || resource.Goal == nil {
					return nil, fmt.Errorf("prerequisite %s has unresolved parent/provider %s", urn, dependency)
				}
				if resource.Goal.Custom && !strings.HasPrefix(resource.Goal.Type, "pulumi:providers:") {
					return nil, fmt.Errorf("prerequisite %s requires explicit parent target %s", urn, dependency)
				}
				selected[dependency] = true
				changed = true
			}
		}
	}
	for urn := range selected {
		goal := resources[urn].Goal
		if goal == nil {
			continue
		}
		dependencies := append([]string{}, goal.Dependencies...)
		for _, values := range goal.PropertyDependencies {
			dependencies = append(dependencies, values...)
		}
		for _, dependency := range dependencies {
			if !selected[dependency] {
				return nil, fmt.Errorf("prerequisite %s depends on untargeted %s; declare it explicitly", urn, dependency)
			}
		}
	}
	result := make([]string, 0, len(selected))
	for urn := range selected {
		result = append(result, urn)
	}
	sort.Strings(result)
	return result, nil
}
func providerURN(reference string) string {
	if reference == "" {
		return ""
	}
	index := strings.LastIndex(reference, "::")
	if index < 0 {
		return reference
	}
	return reference[:index]
}
