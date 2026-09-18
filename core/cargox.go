package core

// Cargo extension responsibilities:
//   - detect the repository convention for Cargo template registries;
//   - apply one disposable version to every declared package;
//   - verify ownership before skipping an existing crate/version pair;
//   - invoke each package's publish task and restore all source metadata.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

var (
	cargoPackageNamePattern    = regexp.MustCompile(`^(\s*name\s*=\s*)"[^"]*"`)
	cargoPackageVersionPattern = regexp.MustCompile(`^(\s*version\s*=\s*)"[^"]*"`)
	cratesAPIBaseURL           = "https://crates.io/api/v1"
)

type cargoFileBackup struct {
	path   string
	data   []byte
	mode   os.FileMode
	exists bool
}

type cargoPublication struct {
	directory string
	name      string
	exists    bool
}

// isCargoRegistry reports whether root follows Premise's Cargo registry convention.
func isCargoRegistry(root string) (bool, error) {
	path := filepath.Join(root, "templates", "Cargo.toml")
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().IsRegular(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func publishCargoPackages(ctx context.Context, root, version, task string, stdout, stderr io.Writer) (resultErr error) {
	version = strings.TrimPrefix(version, "v")
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		return fmt.Errorf("invalid Cargo release version %q: %w", version, err)
	}
	switch task {
	case "publish:rc":
		if parsed.Prerelease() == "" {
			return fmt.Errorf("Cargo RC publication requires a prerelease version, got %s", version)
		}
	case "publish":
		if parsed.Prerelease() != "" || parsed.Metadata() != "" {
			return fmt.Errorf("Cargo stable publication requires a stable version, got %s", version)
		}
	default:
		return fmt.Errorf("unsupported Cargo publication task %q", task)
	}

	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return fmt.Errorf("load Cargo registry manifest: %w", err)
	}
	if len(manifest.Templates) == 0 {
		return errors.New("Cargo registry manifest declares no templates")
	}

	backups := make([]cargoFileBackup, 0, len(manifest.Templates)+1)
	for _, template := range manifest.Templates {
		path := filepath.Join(root, "templates", template.Name, "Cargo.toml")
		backup, err := backupCargoFile(path)
		if err != nil {
			return err
		}
		backups = append(backups, backup)
	}
	lockPath := filepath.Join(root, "templates", "Cargo.lock")
	lockBackup, err := backupCargoFile(lockPath)
	if err != nil {
		return err
	}
	backups = append(backups, lockBackup)
	defer func() {
		resultErr = errors.Join(resultErr, restoreCargoFiles(backups))
	}()

	for _, backup := range backups[:len(manifest.Templates)] {
		if err := setCargoPackageVersion(backup.path, version); err != nil {
			return err
		}
	}
	lockRunner := miseRunner{Stdout: stdout, Stderr: stderr}
	templatesRoot := filepath.Join(root, "templates")
	if err := lockRunner.run(ctx, templatesRoot, nil, "exec", "--", "cargo", "generate-lockfile"); err != nil {
		return fmt.Errorf("regenerate Cargo lockfile: %w", err)
	}

	token := os.Getenv("CRATES_TOKEN")
	if token == "" {
		return errors.New("CRATES_TOKEN is required for Cargo publication")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	preflight := miseRunner{Stderr: stderr}
	publisher := miseRunner{Stdout: stdout, Stderr: stderr}
	publications := make([]cargoPublication, 0, len(manifest.Templates))
	userID := uint64(0)
	for _, template := range manifest.Templates {
		directory := filepath.Join(root, "templates", template.Name)
		name, err := cargoPackageName(filepath.Join(directory, "Cargo.toml"))
		if err != nil {
			return err
		}
		if name != template.Name {
			return fmt.Errorf("Cargo package %q does not match declared template %q", name, template.Name)
		}
		taskExists, err := preflight.taskExists(ctx, directory, task)
		if err != nil {
			return fmt.Errorf("inspect Cargo package %s task %s: %w", name, task, err)
		}
		if !taskExists {
			return fmt.Errorf("Cargo package %s does not define task %s", name, task)
		}
		crateExists, err := cargoCrateExists(ctx, client, name)
		if err != nil {
			return err
		}
		versionExists, err := cargoVersionExists(ctx, client, name, version)
		if err != nil {
			return err
		}
		if versionExists && !crateExists {
			return fmt.Errorf("crates.io reported version %s for missing crate %s", version, name)
		}
		if crateExists {
			if userID == 0 {
				userID, err = authenticatedCargoUserID(ctx, client, token)
				if err != nil {
					return err
				}
			}
			owned, err := cargoCrateOwnedBy(ctx, client, token, name, userID)
			if err != nil {
				return err
			}
			if !owned {
				return fmt.Errorf("crate %s exists but the authenticated crates.io user is not an owner", name)
			}
		}
		publications = append(publications, cargoPublication{directory: directory, name: name, exists: versionExists})
	}
	for _, publication := range publications {
		if publication.exists {
			fmt.Fprintf(stdout, "skip: %s %s already published\n", publication.name, version)
			continue
		}
		fmt.Fprintf(stdout, "%s: %s %s\n", task, publication.name, version)
		environment := []string{
			"RELEASE_VERSION=" + version,
			"CARGO_REGISTRY_TOKEN=" + token,
		}
		if err := publisher.run(ctx, publication.directory, environment, "run", task); err != nil {
			return fmt.Errorf("publish Cargo package %s: %w", publication.name, err)
		}
	}
	return nil
}

func backupCargoFile(path string) (cargoFileBackup, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cargoFileBackup{path: path}, nil
	}
	if err != nil {
		return cargoFileBackup{}, fmt.Errorf("back up Cargo file %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return cargoFileBackup{}, fmt.Errorf("inspect Cargo file %s: %w", path, err)
	}
	return cargoFileBackup{path: path, data: data, mode: info.Mode().Perm(), exists: true}, nil
}

func restoreCargoFiles(backups []cargoFileBackup) error {
	var failures []error
	for _, backup := range backups {
		if !backup.exists {
			if err := os.Remove(backup.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("remove generated Cargo file %s: %w", backup.path, err))
			}
			continue
		}
		if err := os.WriteFile(backup.path, backup.data, backup.mode); err != nil {
			failures = append(failures, fmt.Errorf("restore Cargo file %s: %w", backup.path, err))
		}
	}
	return errors.Join(failures...)
}

func setCargoPackageVersion(path, version string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Cargo manifest %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	inPackage := false
	replaced := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inPackage = trimmed == "[package]"
			continue
		}
		if inPackage && cargoPackageVersionPattern.MatchString(line) {
			lines[index] = cargoPackageVersionPattern.ReplaceAllString(line, `${1}"`+version+`"`)
			replaced = true
			break
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

func cargoPackageName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Cargo manifest %s: %w", path, err)
	}
	inPackage := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inPackage = trimmed == "[package]"
			continue
		}
		if inPackage && cargoPackageNamePattern.MatchString(line) {
			match := cargoPackageNamePattern.FindStringSubmatch(line)
			quoted := strings.TrimSpace(strings.TrimPrefix(match[0], match[1]))
			return strings.Trim(quoted, `"`), nil
		}
	}
	return "", fmt.Errorf("Cargo manifest %s has no [package] name", path)
}

func cargoCrateExists(ctx context.Context, client *http.Client, name string) (bool, error) {
	return cargoResourceExists(ctx, client, cratesAPIBaseURL+"/crates/"+name, "crate "+name)
}

func cargoVersionExists(ctx context.Context, client *http.Client, name, version string) (bool, error) {
	return cargoResourceExists(ctx, client, cratesAPIBaseURL+"/crates/"+name+"/"+version, "crate version "+name+" "+version)
}

func cargoResourceExists(ctx context.Context, client *http.Client, resourceURL, description string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", "premise-release-check")
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("check crates.io %s: %w", description, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("check crates.io %s: HTTP %d", description, response.StatusCode)
	}
}

func authenticatedCargoUserID(ctx context.Context, client *http.Client, token string) (uint64, error) {
	var payload struct {
		User struct {
			ID uint64 `json:"id"`
		} `json:"user"`
	}
	if err := cargoAPIJSON(ctx, client, token, "/me", &payload); err != nil {
		return 0, fmt.Errorf("read authenticated crates.io user: %w", err)
	}
	if payload.User.ID == 0 {
		return 0, errors.New("crates.io user response omitted an id")
	}
	return payload.User.ID, nil
}

func cargoCrateOwnedBy(ctx context.Context, client *http.Client, token, name string, userID uint64) (bool, error) {
	var payload struct {
		Users []struct {
			ID uint64 `json:"id"`
		} `json:"users"`
	}
	if err := cargoAPIJSON(ctx, client, token, "/crates/"+name+"/owners", &payload); err != nil {
		return false, fmt.Errorf("read crates.io owners for %s: %w", name, err)
	}
	for _, user := range payload.Users {
		if user.ID == userID {
			return true, nil
		}
	}
	return false, nil
}

func cargoAPIJSON(ctx context.Context, client *http.Client, token, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cratesAPIBaseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("User-Agent", "premise-release-check")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
