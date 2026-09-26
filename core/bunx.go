package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// BunPackage is package metadata read from package.json using Bun's npm
// publication conventions.
type BunPackage struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Private       bool   `json:"private"`
	PublishConfig struct {
		Access   string `json:"access"`
		Registry string `json:"registry"`
	} `json:"publishConfig"`
}

// ReadBunPackage parses package.json at path. A missing file returns
// found=false. It validates only the format's required package name; release
// policy belongs to the Bun plugin.
func ReadBunPackage(path string) (pkg BunPackage, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return BunPackage{}, false, nil
	}
	if err != nil {
		return BunPackage{}, false, fmt.Errorf("read Bun package %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return BunPackage{}, false, fmt.Errorf("parse Bun package %s: %w", path, err)
	}
	if pkg.Name == "" {
		return BunPackage{}, false, fmt.Errorf("Bun package %s has no name", path)
	}
	return pkg, true, nil
}
