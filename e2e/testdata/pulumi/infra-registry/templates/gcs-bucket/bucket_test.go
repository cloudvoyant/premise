package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Public API -----------------------------------------------------------------

func TestBucketPerEnvironment(t *testing.T) {
	for _, stack := range []string{"dev", "prod", "shared"} {
		t.Run(stack, func(t *testing.T) {
			mocks := &bucketMocks{}
			err := pulumi.RunErr(func(ctx *pulumi.Context) error {
				return defineBucket(ctx, "test-project", "US", nil)
			}, pulumi.WithMocks("test", stack, mocks))
			if err != nil {
				t.Fatal(err)
			}
			if mocks.buckets != 1 {
				t.Fatalf("got %d buckets, want 1", mocks.buckets)
			}
		})
	}
}

// TestBoundArtifacts checks provider object registration and content updates.
func TestBoundArtifacts(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "index.html")
	var previousHash, previousID string
	for _, content := range []string{"first build", "updated build"} {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		mocks := &bucketMocks{}
		err := pulumi.RunErr(func(ctx *pulumi.Context) error {
			return defineBucket(ctx, "test-project", "US", map[string]string{"site": directory})
		}, pulumi.WithMocks("environment", "dev", mocks))
		if err != nil {
			t.Fatal(err)
		}
		if mocks.buckets != 1 || len(mocks.objects) != 1 {
			t.Fatalf("expected one bucket and one object: %v", mocks)
		}
		for id, object := range mocks.objects {
			if object["name"].StringValue() != "site/index.html" {
				t.Fatalf("unexpected object key: %v", object)
			}
			if !object["source"].IsAsset() {
				t.Fatal("object must carry the built file asset")
			}
			hash := object["sourceMd5hash"].StringValue()
			if hash == "" || hash == previousHash {
				t.Fatal("changed artifact content must change the provider checksum")
			}
			if previousID != "" && id != previousID {
				t.Fatal("object identity must remain stable across content updates")
			}
			previousID, previousHash = id, hash
		}
	}
}

func TestMissingBoundArtifactStopsRegistration(t *testing.T) {
	mocks := &bucketMocks{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return defineBucket(ctx, "test-project", "US", map[string]string{"site": filepath.Join(t.TempDir(), "missing")})
	}, pulumi.WithMocks("environment", "dev", mocks))
	if err == nil || mocks.buckets != 0 {
		t.Fatalf("missing artifact should fail before registration: %v", err)
	}
}

// TestProgramConsumesBindingConfiguration exercises the actual program entrypoint.
func TestProgramConsumesBindingConfiguration(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"), []byte("built app"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifacts, err := json.Marshal(map[string]string{"site": directory})
	if err != nil {
		t.Fatal(err)
	}
	mocks := &bucketMocks{}
	err = pulumi.RunErr(runBucket, pulumi.WithMocks("environment", "dev", mocks), func(info *pulumi.RunInfo) {
		info.Config = map[string]string{"gcp:project": "test-project", "environment:location": "US", "environment:artifacts": string(artifacts)}
	})
	if err != nil {
		t.Fatal(err)
	}
	if mocks.buckets != 1 || len(mocks.objects) != 1 {
		t.Fatalf("binding input did not register bucket and artifact: %v", mocks)
	}
}

// Private helpers ------------------------------------------------------------

type bucketMocks struct {
	mu      sync.Mutex
	buckets int
	objects map[string]resource.PropertyMap
}

func (m *bucketMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func (m *bucketMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if args.TypeToken == "gcp:storage/bucketObject:BucketObject" {
		if m.objects == nil {
			m.objects = map[string]resource.PropertyMap{}
		}
		m.objects[args.Name] = args.Inputs
	}
	if args.TypeToken == "premise:storage:Bucket" {
		return "", nil, fmt.Errorf("template must use published providers directly")
	}
	if args.TypeToken == "gcp:storage/bucket:Bucket" {
		m.buckets++
		if args.Inputs["project"].StringValue() != "test-project" || args.Inputs["location"].StringValue() != "US" {
			return "", nil, fmt.Errorf("unexpected project or location: %v", args.Inputs)
		}
		if !args.Inputs["uniformBucketLevelAccess"].BoolValue() || args.Inputs["publicAccessPrevention"].StringValue() != "enforced" || args.Inputs["forceDestroy"].BoolValue() {
			return "", nil, fmt.Errorf("unexpected bucket policy: %v", args.Inputs)
		}
		args.Inputs["name"] = resource.NewStringProperty(args.Name + "-physical")
	}
	return args.Name + "-id", args.Inputs, nil
}
