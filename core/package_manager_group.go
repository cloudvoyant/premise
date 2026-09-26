package core

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// packageManagerGroup combines every root-level package manager into one
// release. It retains registration order for publication and artifact builds.
type packageManagerGroup struct {
	plugins []PackageManagerPlugin
}

func (g packageManagerGroup) ID() string {
	ids := make([]string, 0, len(g.plugins))
	for _, plugin := range g.plugins {
		ids = append(ids, plugin.ID())
	}
	return strings.Join(ids, ",")
}

func (g packageManagerGroup) Detect(root string) (bool, error) {
	for _, plugin := range g.plugins {
		matched, err := plugin.Detect(root)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

func (g packageManagerGroup) IsPublic(root string, template Template) (bool, error) {
	for _, plugin := range g.plugins {
		public, err := plugin.IsPublic(root, template)
		if err != nil || public {
			return public, err
		}
	}
	return false, nil
}

func (g packageManagerGroup) ShouldPublishPackage(root string, template Template) (bool, error) {
	for _, plugin := range g.plugins {
		publish, err := plugin.ShouldPublishPackage(root, template)
		if err != nil || publish {
			return publish, err
		}
	}
	return false, nil
}

func (g packageManagerGroup) SupportsPackages() bool {
	for _, plugin := range g.plugins {
		if plugin.SupportsPackages() {
			return true
		}
	}
	return false
}

func (g packageManagerGroup) PublishPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) error {
	for _, plugin := range g.plugins {
		if !plugin.SupportsPackages() {
			continue
		}
		if err := plugin.PublishPackages(ctx, root, version, task, stdout, stderr); err != nil {
			return fmt.Errorf("publish %s packages: %w", plugin.ID(), err)
		}
	}
	return nil
}

func (g packageManagerGroup) ReleaseWorkspace(ctx context.Context, root string, stdout, stderr io.Writer) (string, bool, error) {
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return "", false, err
	}
	workspace := ""
	serial := false
	for _, plugin := range g.plugins {
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

// CreateGoReleaserConfig merges build and archive sections from every plugin.
// Reject unknown sections rather than silently dropping a plugin's settings.
func (g packageManagerGroup) CreateGoReleaserConfig(root string, manifest Config) (string, error) {
	builds := &yaml.Node{Kind: yaml.SequenceNode}
	archives := &yaml.Node{Kind: yaml.SequenceNode}
	seen := map[string]map[string]bool{"builds": {}, "archives": {}}
	for _, plugin := range g.plugins {
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
		for i := 0; i < len(mapping.Content); i += 2 {
			section := mapping.Content[i].Value
			items := mapping.Content[i+1]
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
				for n := 0; n < len(item.Content); n += 2 {
					if item.Content[n].Value == "id" {
						id := item.Content[n+1].Value
						if seen[section][id] {
							return "", fmt.Errorf("duplicate %s artifact ID %q", section, id)
						}
						seen[section][id] = true
					}
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
