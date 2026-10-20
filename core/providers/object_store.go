package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StoredObject carries the version required for conditional updates and deletion.
type StoredObject struct {
	Data    []byte
	Version string
}

// ObjectStore persists deployment records in an existing file, S3, or GCS prefix.
// It does not create a bucket or manage Pulumi resource state.
type ObjectStore struct {
	URL string
}

// ErrObjectNotFound distinguishes an absent record from denied or broken storage.
var ErrObjectNotFound = errors.New("deployment record does not exist")

// Public API -----------------------------------------------------------------

// Read returns one complete object and its immutable version.
func (store ObjectStore) Read(ctx context.Context, key string) (StoredObject, error) {
	u, err := store.location(key)
	if err != nil {
		return StoredObject{}, err
	}
	switch u.Scheme {
	case "s3":
		return readS3Object(ctx, u)
	case "gs":
		return readGCSObject(ctx, u)
	case "file":
		data, err := os.ReadFile(filepath.FromSlash(u.Path))
		if errors.Is(err, os.ErrNotExist) {
			return StoredObject{}, ErrObjectNotFound
		}
		if err != nil {
			return StoredObject{}, err
		}
		return StoredObject{Data: data, Version: objectDigest(data)}, nil
	default:
		return StoredObject{}, fmt.Errorf("deployment records require an s3://, gs://, or file:// prefix")
	}
}

// Put writes data conditionally. An empty version allows replacement, "*" requires
// an absent object, and any other value requires the version returned by Read.
func (store ObjectStore) Put(ctx context.Context, key string, data []byte, version string) error {
	u, err := store.location(key)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "s3":
		return writeS3Object(ctx, u, data, version)
	case "gs":
		return writeGCSObject(ctx, u, data, version)
	case "file":
		return mutateFileObject(filepath.FromSlash(u.Path), data, version, false)
	default:
		return fmt.Errorf("deployment records require an s3://, gs://, or file:// prefix")
	}
}

// Delete removes only the object version returned by Read.
func (store ObjectStore) Delete(ctx context.Context, key, version string) error {
	if version == "" || version == "*" {
		return fmt.Errorf("deleting a deployment record requires its exact version")
	}
	u, err := store.location(key)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "s3":
		_, err = objectCommand(ctx, "aws", "s3api", "delete-object", "--bucket", u.Host, "--key", strings.TrimPrefix(u.Path, "/"), "--if-match", version, "--no-cli-pager")
		return err
	case "gs":
		_, err = objectCommand(ctx, "gcloud", "storage", "rm", u.String(), "--if-generation-match="+version, "--quiet")
		return err
	case "file":
		return mutateFileObject(filepath.FromSlash(u.Path), nil, version, true)
	default:
		return fmt.Errorf("deployment records require an s3://, gs://, or file:// prefix")
	}
}

// Private helpers ------------------------------------------------------------

type objectCommandError struct {
	name       string
	underlying error
	diagnostic string
}

func (store ObjectStore) location(key string) (*url.URL, error) {
	if key == "" || strings.Contains(key, "\\") || filepath.IsAbs(key) {
		return nil, fmt.Errorf("invalid deployment record key")
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("invalid deployment record key")
		}
	}
	u, err := url.Parse(store.URL)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid deployment record store URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + key
	return u, nil
}

func objectDigest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (err *objectCommandError) Error() string {
	return fmt.Sprintf("%s deployment record command failed: %v; check credentials, storage permissions, and conditional-write support", err.name, err.underlying)
}

func objectCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var output, diagnostic bytes.Buffer
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		return nil, &objectCommandError{name: name, underlying: err, diagnostic: diagnostic.String()}
	}
	return output.Bytes(), nil
}

func objectNotFound(err error) bool {
	var failure *objectCommandError
	if !errors.As(err, &failure) {
		return false
	}
	for _, marker := range []string{"(404)", "NoSuchKey", "No URLs matched", "matched no objects", "HTTPError 404", "not found: 404"} {
		if strings.Contains(failure.diagnostic, marker) {
			return true
		}
	}
	return false
}

func temporaryObjectBody(data []byte) (string, func(), error) {
	file, err := os.CreateTemp("", "premise-record-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.Remove(file.Name()) }
	if _, err := file.Write(data); err != nil {
		file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}

func mutateFileObject(path string, data []byte, version string, remove bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	guard, err := os.OpenFile(path+".guard", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("deployment record is being updated: %w", err)
	}
	guard.Close()
	defer os.Remove(path + ".guard")
	previous, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if version == "*" && err == nil {
		return fmt.Errorf("deployment record already exists")
	}
	if version != "" && version != "*" && (err != nil || objectDigest(previous) != version) {
		return fmt.Errorf("deployment record changed since it was read")
	}
	if remove {
		return os.Remove(path)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".deployment-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
