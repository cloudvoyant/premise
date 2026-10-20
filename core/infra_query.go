package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// InfraQuery selects one exported value or one saved resource property.
type InfraQuery struct {
	Output      string
	Resource    string
	Type        string
	Property    string
	JSON        bool
	ShowSecrets bool
}

// Public API -----------------------------------------------------------------

// QueryInfrastructure reads saved state without refreshing or applying resources.
// A secret value requires an explicit ShowSecrets request.
func QueryInfrastructure(ctx context.Context, root string, options InfraOptions, query InfraQuery, stdout, stderr io.Writer) error {
	if (query.Output == "") == (query.Resource == "") {
		return fmt.Errorf("select exactly one --output or --resource")
	}
	if query.Resource != "" && query.Property == "" {
		return fmt.Errorf("--resource requires --property, such as id or arn")
	}
	if query.Output != "" && (query.Property != "" || query.Type != "") {
		return fmt.Errorf("--property and --type apply only to resource queries")
	}
	destination, err := ResolveInfrastructure(root, options)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	runner := infraCommandRunner{stdout: &output, stderr: stderr, private: true}
	environment := map[string]string{"PULUMI_BACKEND_URL": destination.BackendURL}
	if err := runner.run(ctx, destination.Directory, environment, "pulumi", "login", destination.BackendURL, "--non-interactive"); err != nil {
		return err
	}
	args := []string{"stack", "output", "--json"}
	if query.Resource != "" {
		args = []string{"stack", "export"}
	}
	args = append(args, "--stack", destination.Stack, "--non-interactive")
	if query.ShowSecrets {
		args = append(args, "--show-secrets")
	}
	if err := runner.run(ctx, destination.Directory, environment, "pulumi", args...); err != nil {
		return fmt.Errorf("query stack %s: %w", destination.Stack, err)
	}
	var value any
	if query.Output != "" {
		var outputs map[string]any
		if err := json.Unmarshal(output.Bytes(), &outputs); err != nil {
			return fmt.Errorf("read Pulumi stack outputs: %w", err)
		}
		var exists bool
		value, exists = outputs[query.Output]
		if !exists {
			return fmt.Errorf("stack %s has no output %q", destination.Stack, query.Output)
		}
	} else {
		var state pulumiCheckpoint
		if err := json.Unmarshal(output.Bytes(), &state); err != nil {
			return fmt.Errorf("read Pulumi stack resources: %w", err)
		}
		var matches []pulumiSavedResource
		for _, resource := range state.Deployment.Resources {
			// Interrupted replacements can leave an old resource with the same URN.
			// Queries select the current resource, not the pending deletion.
			if resource.Delete {
				continue
			}
			if resource.URN != query.Resource && !strings.HasSuffix(resource.URN, "::"+query.Resource) {
				continue
			}
			if query.Type != "" && query.Type != resource.Type {
				continue
			}
			matches = append(matches, resource)
		}
		if len(matches) == 0 {
			return fmt.Errorf("stack %s has no resource matching %q", destination.Stack, query.Resource)
		}
		if len(matches) != 1 {
			return fmt.Errorf("resource %q matches %d resources; select its full URN or add --type", query.Resource, len(matches))
		}
		resource := matches[0]
		switch query.Property {
		case "id":
			value = resource.ID
		case "urn":
			value = resource.URN
		case "type":
			value = resource.Type
		default:
			value = resource.Outputs
			for _, key := range strings.Split(query.Property, ".") {
				if wrapped, ok := value.(map[string]any); ok && wrapped["4dabf18193072939515e22adb298388d"] == "1b47061264138c4ac30d75fd1eb44270" {
					value, err = readablePulumiValue(value, query.ShowSecrets)
					if err != nil {
						return err
					}
				}
				fields, ok := value.(map[string]any)
				if !ok {
					return fmt.Errorf("resource %q has no property %q", query.Resource, query.Property)
				}
				value, ok = fields[key]
				if !ok {
					return fmt.Errorf("resource %q has no property %q", query.Resource, query.Property)
				}
			}
		}
	}
	value, err = readablePulumiValue(value, query.ShowSecrets)
	if err != nil {
		return err
	}
	if !query.JSON {
		if text, ok := value.(string); ok {
			_, err := fmt.Fprintln(stdout, text)
			return err
		}
	}
	return json.NewEncoder(stdout).Encode(value)
}

// Private helpers ------------------------------------------------------------

type pulumiCheckpoint struct {
	Deployment struct {
		Resources []pulumiSavedResource `json:"resources"`
	} `json:"deployment"`
}

type pulumiSavedResource struct {
	Delete  bool           `json:"delete"`
	URN     string         `json:"urn"`
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Outputs map[string]any `json:"outputs"`
}

func readablePulumiValue(value any, showSecrets bool) (any, error) {
	switch typed := value.(type) {
	case string:
		if typed == "[secret]" && !showSecrets {
			return nil, fmt.Errorf("value is secret; request --show-secrets explicitly")
		}
	case map[string]any:
		if typed["4dabf18193072939515e22adb298388d"] == "1b47061264138c4ac30d75fd1eb44270" {
			if !showSecrets {
				return nil, fmt.Errorf("value is secret; request --show-secrets explicitly")
			}
			plaintext, exists := typed["value"]
			if !exists {
				return nil, fmt.Errorf("Pulumi did not return a decrypted secret value")
			}
			return readablePulumiValue(plaintext, true)
		}
		for key, child := range typed {
			readable, err := readablePulumiValue(child, showSecrets)
			if err != nil {
				return nil, err
			}
			typed[key] = readable
		}
	case []any:
		for i, child := range typed {
			readable, err := readablePulumiValue(child, showSecrets)
			if err != nil {
				return nil, err
			}
			typed[i] = readable
		}
	}
	return value, nil
}
