package core

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var (
	cargoPackageVersionPattern = regexp.MustCompile(`^(\s*version\s*=\s*)(?:"[^"]*"|'[^']*')`)
	cargoInlineVersionPattern  = regexp.MustCompile(`(\bversion\s*=\s*)(?:"[^"]*"|'[^']*')`)
	cargoInlinePathPattern     = regexp.MustCompile(`(\bpath\s*=\s*(?:"[^"]*"|'[^']*'))`)
)

// CargoPackage holds metadata from a Cargo.toml [package] table. PublishCratesIO
// reports whether Cargo permits publication to crates.io; PublishRegistries
// lists explicitly configured named registries from [package].publish.
type CargoPackage struct {
	Name              string
	Version           string
	PublishCratesIO   bool
	PublishRegistries []string
	PathDependencies  []string
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
		Dependencies      map[string]any `toml:"dependencies"`
		BuildDependencies map[string]any `toml:"build-dependencies"`
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
	var publishRegistries []string
	switch publish := manifest.Package.Publish.(type) {
	case nil:
	case bool:
		publishCratesIO = publish
	case []any:
		publishCratesIO = false
		for _, registry := range publish {
			name, ok := registry.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return CargoPackage{}, false, fmt.Errorf("Cargo manifest %s has invalid [package] publish registry entry", path)
			}
			if !slices.Contains(publishRegistries, name) {
				publishRegistries = append(publishRegistries, name)
			}
			if name == "crates-io" {
				publishCratesIO = true
			}
		}
	default:
		return CargoPackage{}, false, fmt.Errorf("Cargo manifest %s has invalid [package] publish value", path)
	}
	pathDeps := collectCargoPathDependencies(manifest.Dependencies, manifest.BuildDependencies)
	return CargoPackage{
		Name:              manifest.Package.Name,
		Version:           version,
		PublishCratesIO:   publishCratesIO,
		PublishRegistries: publishRegistries,
		PathDependencies:  pathDeps,
	}, true, nil
}

// SetCargoPackageVersion updates [package].version in Cargo.toml and, when
// siblingVersions is non-empty, updates matching inline path dependency
// versions in [dependencies] and [build-dependencies] while preserving formatting.
func SetCargoPackageVersion(path, version string, siblingVersions map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Cargo manifest %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	section := ""
	replaced := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section = strings.TrimSpace(strings.SplitN(trimmed, "#", 2)[0])
			continue
		}
		if section == "[package]" && !replaced {
			match := cargoPackageVersionPattern.FindStringSubmatchIndex(line)
			if match != nil {
				lines[index] = line[match[2]:match[3]] + strconv.Quote(version) + line[match[1]:]
				replaced = true
				continue
			}
		}
		if (section == "[dependencies]" || section == "[build-dependencies]") && len(siblingVersions) > 0 {
			lines[index] = rewriteCargoSiblingDependencyLine(line, siblingVersions)
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

// Private helpers ------------------------------------------------------------

func collectCargoPathDependencies(sections ...map[string]any) []string {
	seen := map[string]struct{}{}
	for _, section := range sections {
		for key, raw := range section {
			table, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if p, ok := table["path"].(string); !ok || strings.TrimSpace(p) == "" {
				continue
			}
			name := key
			if pkgName, ok := table["package"].(string); ok && strings.TrimSpace(pkgName) != "" {
				name = pkgName
			}
			seen[name] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func rewriteCargoSiblingDependencyLine(line string, siblingVersions map[string]string) string {
	eq := strings.Index(line, "=")
	if eq <= 0 {
		return line
	}
	key := strings.TrimSpace(line[:eq])
	rhs := line[eq+1:]
	if !strings.Contains(rhs, "{") || !cargoInlinePathPattern.MatchString(rhs) {
		return line
	}
	targetName := key
	if pkgMatch := regexp.MustCompile(`\bpackage\s*=\s*(?:"([^"]*)"|'([^']*)')`).FindStringSubmatch(rhs); len(pkgMatch) == 3 {
		if pkgMatch[1] != "" {
			targetName = pkgMatch[1]
		} else if pkgMatch[2] != "" {
			targetName = pkgMatch[2]
		}
	}
	ver, ok := siblingVersions[targetName]
	if !ok {
		return line
	}
	if cargoInlineVersionPattern.MatchString(rhs) {
		updated := cargoInlineVersionPattern.ReplaceAllString(rhs, "${1}"+strconv.Quote(ver))
		return line[:eq+1] + updated
	}
	updated := cargoInlinePathPattern.ReplaceAllString(rhs, "version = "+strconv.Quote(ver)+", ${1}")
	return line[:eq+1] + updated
}
