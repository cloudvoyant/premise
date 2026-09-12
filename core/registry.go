package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
)

// Registry lists templates available from one source.
type Registry struct {
	Templates []Template
}

func LoadRegistry(sourceRoot string) (Registry, error) {
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return Registry{}, fmt.Errorf("resolve registry root: %w", err)
	}
	manifest, err := LoadManifest(filepath.Join(root, ManifestFilename))
	if err != nil {
		return Registry{}, fmt.Errorf("load registry manifest: %w", err)
	}
	templates := append([]Template{}, manifest.Templates...)
	sort.Slice(templates, func(i, j int) bool {
		return templates[i].Name < templates[j].Name
	})
	return Registry{Templates: templates}, nil
}

func DefaultRegistry(ctx context.Context) (Registry, error) {
	root, err := resolveRepository(ctx, NativeTemplateSource)
	if err != nil {
		return Registry{}, err
	}
	return LoadRegistry(root)
}

func (registry Registry) Names() []string {
	names := make([]string, len(registry.Templates))
	for index, template := range registry.Templates {
		names[index] = template.Name
	}
	return names
}

func resolveRepository(ctx context.Context, source string) (string, error) {
	repositoryURL := normalizeRepositoryURL(source)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	cacheRoot := filepath.Join(home, ".premise", "templates")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return "", fmt.Errorf("create template cache: %w", err)
	}
	digest := sha256.Sum256([]byte(repositoryURL))
	cachePath := filepath.Join(cacheRoot, hex.EncodeToString(digest[:16]))

	if _, err := os.Stat(cachePath); err == nil {
		if err := refreshRepository(ctx, cachePath); err != nil {
			return "", err
		}
		return cachePath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect template cache: %w", err)
	}

	tempRoot, err := os.MkdirTemp(cacheRoot, ".clone-*")
	if err != nil {
		return "", fmt.Errorf("create temporary clone directory: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	tempRepository := filepath.Join(tempRoot, "repository")
	if _, err := git.PlainCloneContext(ctx, tempRepository, false, &git.CloneOptions{
		URL:          repositoryURL,
		Depth:        1,
		SingleBranch: true,
	}); err != nil {
		return "", fmt.Errorf("clone template repository: %w", err)
	}
	if err := os.Rename(tempRepository, cachePath); err != nil {
		if _, statErr := os.Stat(cachePath); statErr == nil {
			return cachePath, nil
		}
		return "", fmt.Errorf("install template cache: %w", err)
	}
	return cachePath, nil
}

func normalizeRepositoryURL(source string) string {
	trimmed := strings.TrimSpace(source)
	if !strings.Contains(trimmed, "://") && !strings.Contains(trimmed, "@") && strings.Count(trimmed, "/") == 1 {
		return "https://github.com/" + strings.TrimSuffix(trimmed, ".git") + ".git"
	}
	return trimmed
}

func refreshRepository(ctx context.Context, path string) error {
	repository, err := git.PlainOpen(path)
	if err != nil {
		return fmt.Errorf("open cached template repository: %w", err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		return fmt.Errorf("open cached template worktree: %w", err)
	}
	if err := worktree.PullContext(ctx, &git.PullOptions{RemoteName: "origin"}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("refresh cached template repository: %w", err)
	}
	return nil
}
