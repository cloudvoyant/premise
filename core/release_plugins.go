package core

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

func releasePlugins(root string) (Config, []PackageManagerPlugin, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return Config{}, nil, fmt.Errorf("load release manifest: %w", err)
	}
	plugins, err := packageManagersForConfig(manifest)
	if err != nil {
		return Config{}, nil, err
	}
	return manifest, plugins, nil
}

func prepareReleaseWorkspace(ctx context.Context, root string, manifest Config, plugins []PackageManagerPlugin, stdout, stderr io.Writer) (string, bool, error) {
	workspace := ""
	serial := false
	for _, plugin := range plugins {
		fragment, err := plugin.CreateGoReleaserConfig(root, manifest)
		if err != nil {
			return "", false, fmt.Errorf("generate %s artifacts: %w", plugin.ID(), err)
		}
		if fragment == "" {
			continue
		}
		directory, needsSerial, err := plugin.ReleaseWorkspace(ctx, root, stdout, stderr)
		if err != nil {
			return "", false, fmt.Errorf("prepare %s workspace: %w", plugin.ID(), err)
		}
		if workspace != "" && filepath.Clean(workspace) != filepath.Clean(directory) {
			return "", false, fmt.Errorf("package managers require different release workspaces: %s and %s", workspace, directory)
		}
		workspace = directory
		serial = serial || needsSerial
	}
	if workspace == "" {
		workspace = root
	}
	return workspace, serial, nil
}

func packageManagerReleaseConfig(root string, manifest Config, plugins []PackageManagerPlugin) (string, error) {
	builds := &yaml.Node{Kind: yaml.SequenceNode}
	archives := &yaml.Node{Kind: yaml.SequenceNode}
	seen := map[string]map[string]bool{"builds": {}, "archives": {}}
	for _, plugin := range plugins {
		fragment, err := plugin.CreateGoReleaserConfig(root, manifest)
		if err != nil {
			return "", fmt.Errorf("generate %s artifacts: %w", plugin.ID(), err)
		}
		if fragment == "" {
			continue
		}
		var document yaml.Node
		if err := yaml.Unmarshal([]byte(fragment), &document); err != nil {
			return "", fmt.Errorf("parse %s artifact configuration: %w", plugin.ID(), err)
		}
		if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			return "", fmt.Errorf("%s artifact configuration must be a YAML mapping", plugin.ID())
		}
		mapping := document.Content[0]
		for index := 0; index < len(mapping.Content); index += 2 {
			section := mapping.Content[index].Value
			items := mapping.Content[index+1]
			var target *yaml.Node
			switch section {
			case "builds":
				target = builds
			case "archives":
				target = archives
			default:
				return "", fmt.Errorf("%s artifact configuration has unsupported section %q", plugin.ID(), section)
			}
			if items.Kind != yaml.SequenceNode {
				return "", fmt.Errorf("%s artifact section %q must be a list", plugin.ID(), section)
			}
			for _, item := range items.Content {
				if item.Kind != yaml.MappingNode {
					return "", fmt.Errorf("%s artifact section %q must contain mappings", plugin.ID(), section)
				}
				for field := 0; field < len(item.Content); field += 2 {
					if item.Content[field].Value != "id" {
						continue
					}
					id := item.Content[field+1].Value
					if seen[section][id] {
						return "", fmt.Errorf("duplicate %s artifact ID %q", section, id)
					}
					seen[section][id] = true
				}
				target.Content = append(target.Content, item)
			}
		}
	}
	if len(builds.Content) == 0 && len(archives.Content) == 0 {
		return "", nil
	}
	result := &yaml.Node{Kind: yaml.MappingNode}
	if len(builds.Content) != 0 {
		result.Content = append(result.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "builds"}, builds)
	}
	if len(archives.Content) != 0 {
		result.Content = append(result.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "archives"}, archives)
	}
	data, err := yaml.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal artifact configuration: %w", err)
	}
	return string(data) + "\n", nil
}
