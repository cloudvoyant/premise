package e2e_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Private helpers ------------------------------------------------------------

func premiseBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("PREMISE_BIN"); binary != "" {
		resolved, err := exec.LookPath(binary)
		if err != nil {
			t.Fatal(err)
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			t.Fatal(err)
		}
		return absolute
	}
	binary := filepath.Join(t.TempDir(), "pm")
	runCommand(t, repositoryRoot(t), "go", "build", "-o", binary, ".")
	return binary
}

func runCommand(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+directory)
	var diagnostics strings.Builder
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s\n%s", name, strings.Join(arguments, " "), err, output, diagnostics.String())
	}
	return string(output)
}

func copyFixture(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() == ".pulumi" || entry.Name() == "node_modules" || (strings.HasPrefix(entry.Name(), "Pulumi.") && entry.Name() != "Pulumi.yaml") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("fixture contains a non-regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s must not exist: %v", path, err)
	}
}

func commandFailure(t *testing.T, directory, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "MISE_TRUSTED_CONFIG_PATHS="+directory)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("%s %s unexpectedly succeeded: %s", name, strings.Join(arguments, " "), output)
	}
	return string(output)
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out))
}
