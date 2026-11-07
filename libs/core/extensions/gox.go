package extensions

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Public API -----------------------------------------------------------------

// GoModule contains the parsed native dependency manifest.
type GoModule struct {
	Path string
	File *modfile.File
}

// ReadGoModule returns found=false when the directory has no manifest.
func ReadGoModule(directory string) (GoModule, bool, error) {
	data, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if os.IsNotExist(err) {
		return GoModule{}, false, nil
	}
	if err != nil {
		return GoModule{}, false, err
	}
	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return GoModule{}, false, err
	}
	if file.Module == nil {
		return GoModule{}, false, fmt.Errorf("%s/go.mod has no module directive", directory)
	}
	if err := module.CheckPath(file.Module.Mod.Path); err != nil {
		return GoModule{}, false, err
	}
	return GoModule{Path: file.Module.Mod.Path, File: file}, true, nil
}

// GoDirectoryHasMain inspects package declarations without compiling source.
func GoDirectoryHasMain(directory string) (bool, error) {
	files, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		return false, err
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.PackageClauseOnly)
		if err != nil {
			return false, err
		}
		if parsed.Name.Name == "main" {
			return true, nil
		}
	}
	return false, nil
}
