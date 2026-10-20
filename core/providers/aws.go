package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Private helpers ------------------------------------------------------------

var awsSecretKeys = []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}

func readS3Object(ctx context.Context, location *url.URL) (StoredObject, error) {
	directory, err := os.MkdirTemp("", "premise-s3-record-*")
	if err != nil {
		return StoredObject{}, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "record")
	metadata, err := objectCommand(ctx, "aws", "s3api", "get-object", "--bucket", location.Host,
		"--key", strings.TrimPrefix(location.Path, "/"), "--output", "json", "--no-cli-pager", path)
	if objectNotFound(err) {
		return StoredObject{}, ErrObjectNotFound
	}
	if err != nil {
		return StoredObject{}, fmt.Errorf("read %s: %w", location, err)
	}
	var version struct {
		ETag string `json:"ETag"`
	}
	if err := json.Unmarshal(metadata, &version); err != nil || version.ETag == "" {
		return StoredObject{}, fmt.Errorf("AWS did not return a deployment record ETag")
	}
	data, err := os.ReadFile(path)
	return StoredObject{Data: data, Version: version.ETag}, err
}

func writeS3Object(ctx context.Context, location *url.URL, data []byte, version string) error {
	path, cleanup, err := temporaryObjectBody(data)
	if err != nil {
		return err
	}
	defer cleanup()
	args := []string{"s3api", "put-object", "--bucket", location.Host,
		"--key", strings.TrimPrefix(location.Path, "/"), "--body", path, "--no-cli-pager"}
	if version == "*" {
		args = append(args, "--if-none-match", "*")
	} else if version != "" {
		args = append(args, "--if-match", version)
	}
	_, err = objectCommand(ctx, "aws", args...)
	if err != nil {
		return fmt.Errorf("write %s: %w", location, err)
	}
	return nil
}
