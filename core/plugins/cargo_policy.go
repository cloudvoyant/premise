package plugins

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudvoyant/premise/core"
)

type cargoTemplatePackage struct {
	Template        core.Template
	Directory       string
	Name            string
	RegistryPublish bool
}

// isCargoRegistry reports whether root follows Premise's Cargo registry convention.
func isCargoRegistry(root string) (bool, error) {
	_, found, err := cargoWorkspaceDirectory(root)
	return found, err
}

// cargoWorkspaceDirectory resolves the aggregate Cargo workspace at the
// registry repository root. Template source directories are independent.
func cargoWorkspaceDirectory(root string) (string, bool, error) {
	path := filepath.Join(root, "Cargo.toml")
	found, err := core.IsRegularFile(path)
	if err != nil || !found {
		return "", false, err
	}
	return root, true, nil
}

func inspectCargoTemplatePackage(root string, template core.Template) (cargoTemplatePackage, bool, error) {
	directory, err := core.TemplateDirectory(root, template.Path)
	if err != nil {
		return cargoTemplatePackage{}, false, fmt.Errorf("resolve Cargo template %s: %w", template.Name, err)
	}
	pkg, found, err := core.ReadCargoPackage(filepath.Join(directory, "Cargo.toml"))
	if err != nil || !found {
		return cargoTemplatePackage{}, found, err
	}
	if pkg.Name != template.Name {
		return cargoTemplatePackage{}, false, fmt.Errorf("Cargo package %q does not match declared template %q", pkg.Name, template.Name)
	}
	return cargoTemplatePackage{
		Template:        template,
		Directory:       directory,
		Name:            pkg.Name,
		RegistryPublish: pkg.PublishCratesIO,
	}, true, nil
}

func cargoTemplateIsPublic(root string, template core.Template) (bool, error) {
	pkg, found, err := inspectCargoTemplatePackage(root, template)
	return found && pkg.RegistryPublish, err
}

func cargoReleaseBuilds(root string, manifest core.Config) (string, error) {
	applications := []string{}
	for _, template := range manifest.DeclaredTemplates() {
		if template.Kind != "app" {
			continue
		}
		pkg, found, err := inspectCargoTemplatePackage(root, template)
		if err != nil {
			return "", err
		}
		if found {
			applications = append(applications, pkg.Name)
		}
	}
	if len(applications) == 0 {
		return "", nil // Cargo libraries may publish without downloadable app artifacts.
	}
	var builder strings.Builder
	builder.WriteString("builds:\n")
	for _, application := range applications {
		fmt.Fprintf(&builder, `  - id: %s
    builder: rust
    binary: %s
    dir: .
    targets:
      - x86_64-unknown-linux-gnu
      - aarch64-unknown-linux-gnu
      - x86_64-apple-darwin
      - aarch64-apple-darwin
    flags:
      - --release
      - -p=%s

`, application, application, application)
	}
	builder.WriteString("archives:\n")
	for _, application := range applications {
		fmt.Fprintf(&builder, `  - id: %s
    ids:
      - %s
    formats:
      - tar.gz
    name_template: "{{ .Binary }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}"

`, application, application)
	}
	return builder.String(), nil
}
