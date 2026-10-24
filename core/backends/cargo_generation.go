package backends

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Public API -----------------------------------------------------------------

func (Cargo) GenerationRootFiles() []string { return []string{"Cargo.toml", "Cargo.lock"} }

func (Cargo) PrepareGeneration(request core.PackageGenerationContext) ([]core.MergeEntry, error) {
	selectedData, err := os.ReadFile(filepath.Join(request.Stage, "Cargo.toml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var selected map[string]any
	if err := toml.Unmarshal(selectedData, &selected); err != nil {
		return nil, fmt.Errorf("selected Cargo.toml: %w", err)
	}
	if _, found := selected["package"]; !found {
		return nil, nil
	}
	if _, found := selected["workspace"]; found {
		return nil, fmt.Errorf("selected Cargo.toml defines a nested workspace; templates must provide a crate that joins the client workspace")
	}
	if err := validateCargoGenerationManifest(request.WorkspaceRoot, request.ProjectPath, selected); err != nil {
		return nil, err
	}
	if request.SharedFiles["Cargo.toml"].Present {
		return nil, fmt.Errorf("registry workspace_files must not include Cargo.toml: Cargo prepares client membership; keep crate dependencies in the selected template")
	}
	original := request.ClientFiles["Cargo.toml"]
	var client struct {
		Workspace *struct {
			Members []string `toml:"members"`
			Exclude []string `toml:"exclude"`
		} `toml:"workspace"`
	}
	if err := toml.Unmarshal(original.Data, &client); err != nil {
		return nil, fmt.Errorf("client Cargo.toml: %w", err)
	}
	var clientValues map[string]any
	if err := toml.Unmarshal(original.Data, &clientValues); err != nil {
		return nil, err
	}
	if err := validateCargoGenerationManifest(request.WorkspaceRoot, "", clientValues); err != nil {
		return nil, err
	}
	members := []string{}
	if client.Workspace != nil {
		for _, pattern := range client.Workspace.Exclude {
			if err := validateGenerationLocalPath(request.WorkspaceRoot, "", pattern); err != nil {
				return nil, err
			}
			excluded, err := generationPatternMatches(pattern, request.ProjectPath)
			if err != nil {
				return nil, err
			}
			if excluded {
				return nil, fmt.Errorf("Cargo.toml workspace.exclude excludes generated crate %s with %q", request.ProjectPath, pattern)
			}
		}
		members = client.Workspace.Members
		alreadyCovered := false
		for _, pattern := range members {
			if err := validateGenerationLocalPath(request.WorkspaceRoot, "", pattern); err != nil {
				return nil, err
			}
			covered, err := generationPatternMatches(pattern, request.ProjectPath)
			if err != nil {
				return nil, err
			}
			alreadyCovered = alreadyCovered || covered
		}
		if alreadyCovered {
			return nil, nil
		}
	}
	if client.Workspace == nil {
		// Older clients generated standalone crates. Creating a root must keep them usable.
		for _, path := range request.ProjectPaths {
			if err := validateGenerationLocalPath(request.WorkspaceRoot, "", path); err != nil {
				return nil, err
			}
			data, err := os.ReadFile(filepath.Join(request.WorkspaceRoot, path, "Cargo.toml"))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var existing map[string]any
			if err := toml.Unmarshal(data, &existing); err != nil {
				return nil, fmt.Errorf("existing crate %s: %w", path, err)
			}
			if _, native := existing["package"]; !native {
				continue
			}
			if _, nested := existing["workspace"]; nested {
				return nil, fmt.Errorf("existing crate %s defines a nested workspace; remove it before joining the client root", path)
			}
			if err := validateCargoGenerationManifest(request.WorkspaceRoot, path, existing); err != nil {
				return nil, err
			}
			members = append(members, filepath.ToSlash(path))
		}
	}
	members = append(members, filepath.ToSlash(request.ProjectPath))
	output, err := cargoGenerationMembers(original.Data, members)
	if err != nil {
		return nil, err
	}
	if !original.Present {
		resolver := "2"
		pkg, _ := selected["package"].(map[string]any)
		if pkg["edition"] == "2024" {
			resolver = "3"
		}
		output = append(output, []byte("resolver = \""+resolver+"\"\n")...)
	}
	var verified map[string]any
	if err := toml.Unmarshal(output, &verified); err != nil {
		return nil, fmt.Errorf("cannot extend Cargo workspace without changing its layout: %w", err)
	}
	return []core.MergeEntry{generationEntry("Cargo.toml", output)}, nil
}

// Private helpers ------------------------------------------------------------

// Replace only workspace.members, leaving every other source byte untouched.
func cargoGenerationMembers(data []byte, members []string) ([]byte, error) {
	encoded, err := json.Marshal(members)
	if err != nil {
		return nil, err
	}
	var parser unstable.Parser
	parser.Reset(data)
	section := ""
	tableEnd := -1
	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table, unstable.ArrayTable:
			section = cargoGenerationKey(expression)
			if section == "workspace" && expression.Kind == unstable.Table {
				first := expression.Child()
				start := int(first.Raw.Offset + first.Raw.Length)
				newline := bytes.IndexByte(data[start:], '\n')
				tableEnd = len(data)
				if newline >= 0 {
					tableEnd = start + newline + 1
				}
			}
		case unstable.KeyValue:
			key := cargoGenerationKey(expression)
			if section != "" {
				key = section + "." + key
			}
			if key != "workspace.members" {
				continue
			}
			value := expression.Value()
			if value.Kind != unstable.Array {
				return nil, fmt.Errorf("Cargo workspace.members must be an array")
			}
			keys := expression.Key()
			keyEnd := 0
			for keys.Next() {
				node := keys.Node()
				keyEnd = int(node.Raw.Offset + node.Raw.Length)
			}
			open := bytes.IndexByte(data[keyEnd:], '[')
			if open < 0 {
				return nil, fmt.Errorf("cannot locate Cargo workspace.members array")
			}
			open += keyEnd
			end := open + 1
			children := value.Children()
			for children.Next() {
				child := children.Node()
				position := int(child.Raw.Offset + child.Raw.Length)
				if position > end {
					end = position
				}
			}
			for end < len(data) {
				if data[end] == '#' {
					for end < len(data) && data[end] != '\n' {
						end++
					}
				} else if data[end] == ']' {
					output := append([]byte(nil), data[:open]...)
					output = append(output, encoded...)
					return append(output, data[end+1:]...), nil
				} else {
					end++
				}
			}
			return nil, fmt.Errorf("cannot locate end of Cargo workspace.members")
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	line := append([]byte("members = "), encoded...)
	line = append(line, '\n')
	if tableEnd >= 0 {
		output := append([]byte(nil), data[:tableEnd]...)
		if len(output) > 0 && output[len(output)-1] != '\n' {
			output = append(output, '\n')
		}
		output = append(output, line...)
		return append(output, data[tableEnd:]...), nil
	}
	output := append([]byte(nil), data...)
	if len(output) > 0 && output[len(output)-1] != '\n' {
		output = append(output, '\n')
	}
	output = append(output, []byte("\n[workspace]\n")...)
	return append(output, line...), nil
}

func cargoGenerationKey(node *unstable.Node) string {
	keys := node.Key()
	var parts []string
	for keys.Next() {
		parts = append(parts, string(keys.Node().Data))
	}
	return strings.Join(parts, ".")
}

func validateCargoGenerationManifest(root, base string, manifest map[string]any) error {
	pkg, _ := manifest["package"].(map[string]any)
	if workspace, ok := pkg["workspace"].(string); ok {
		if err := validateGenerationLocalPath(root, base, workspace); err != nil {
			return err
		}
		if filepath.Clean(filepath.Join(base, workspace)) != "." {
			return fmt.Errorf("Cargo package.workspace must point to the client workspace root")
		}
	}
	if build, ok := pkg["build"].(string); ok {
		if err := validateGenerationLocalPath(root, base, build); err != nil {
			return err
		}
	}
	// Only native dependency/source tables contain filesystem references; metadata is opaque.
	for _, key := range []string{"dependencies", "dev-dependencies", "build-dependencies", "patch", "replace", "lib", "bin", "test", "bench", "example"} {
		if err := validateCargoGenerationPaths(root, base, manifest[key]); err != nil {
			return err
		}
	}
	if workspace, ok := manifest["workspace"].(map[string]any); ok {
		if err := validateCargoGenerationPaths(root, "", workspace["dependencies"]); err != nil {
			return err
		}
	}
	if targets, ok := manifest["target"].(map[string]any); ok {
		for _, target := range targets {
			if object, ok := target.(map[string]any); ok {
				for _, key := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
					if err := validateCargoGenerationPaths(root, base, object[key]); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func validateCargoGenerationPaths(root, base string, value any) error {
	switch object := value.(type) {
	case map[string]any:
		if path, ok := object["path"].(string); ok {
			if err := validateGenerationLocalPath(root, base, path); err != nil {
				return err
			}
		}
		for _, child := range object {
			if err := validateCargoGenerationPaths(root, base, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range object {
			if err := validateCargoGenerationPaths(root, base, child); err != nil {
				return err
			}
		}
	}
	return nil
}
