package core

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var cargoPackageVersionPattern = regexp.MustCompile(`^(\s*version\s*=\s*)(?:"[^"]*"|'[^']*')`)

// CargoPackage holds metadata from a Cargo.toml [package] table. PublishCratesIO
// reports whether Cargo permits publication to crates.io, not whether a release
// workflow should publish this package.
type CargoPackage struct {
	Name            string
	Version         string
	PublishCratesIO bool
}

// ReadCargoPackage parses the [package] table at path. A missing file or a
// virtual workspace without [package] returns found=false. Malformed Cargo
// metadata returns an error; the caller decides how to use the package.
func ReadCargoPackage(path string) (pkg CargoPackage, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return CargoPackage{}, false, nil
	}
	if err != nil {
		return CargoPackage{}, false, fmt.Errorf("read Cargo manifest %s: %w", path, err)
	}
	var manifest struct {
		Package *struct {
			Name    string `toml:"name"`
			Version any    `toml:"version"`
			Publish any    `toml:"publish"`
		} `toml:"package"`
	}
	if err := toml.Unmarshal(data, &manifest); err != nil {
		return CargoPackage{}, false, fmt.Errorf("parse Cargo manifest %s: %w", path, err)
	}
	if manifest.Package == nil {
		return CargoPackage{}, false, nil
	}
	if manifest.Package.Name == "" {
		return CargoPackage{}, false, fmt.Errorf("Cargo manifest %s has no [package] name", path)
	}
	version, ok := manifest.Package.Version.(string)
	if !ok {
		return CargoPackage{}, false, fmt.Errorf("Cargo manifest %s has no [package] version", path)
	}
	publishCratesIO := true
	switch publish := manifest.Package.Publish.(type) {
	case nil:
	case bool:
		publishCratesIO = publish
	case []any:
		publishCratesIO = false
		for _, registry := range publish {
			if registry == "crates-io" {
				publishCratesIO = true
			}
		}
	default:
		return CargoPackage{}, false, fmt.Errorf("Cargo manifest %s has invalid [package] publish value", path)
	}
	return CargoPackage{Name: manifest.Package.Name, Version: version, PublishCratesIO: publishCratesIO}, true, nil
}

// SetCargoPackageVersion updates only the [package] version in Cargo.toml,
// preserving comments and formatting in all other sections.
func SetCargoPackageVersion(path, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Cargo manifest %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	inPackage := false
	replaced := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section := strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0])
			inPackage = section == "[package]"
			continue
		}
		if inPackage {
			match := cargoPackageVersionPattern.FindStringSubmatchIndex(line)
			if match != nil {
				lines[index] = line[match[2]:match[3]] + strconv.Quote(version) + line[match[1]:]
				replaced = true
				break
			}
		}
	}
	if !replaced {
		return fmt.Errorf("Cargo manifest %s has no [package] version", path)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return fmt.Errorf("write Cargo manifest %s: %w", path, err)
	}
	return nil
}
