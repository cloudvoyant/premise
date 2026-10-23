package core

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Fixtures -------------------------------------------------------------------

type repositoryTestProvider struct{ testPackageManager }

// Tests ----------------------------------------------------------------------

func TestRepositoryPublicationPreflightsTheSelectedProviderBeforeTagging(t *testing.T) {
	root := writeTaggedCargoReleaseFixture(t)
	manifest := NewManifest("future-packages")
	manifest.Workspace.PackageManagers = []string{"future"}
	if err := SaveManifest(filepath.Join(root, ManifestFilename), manifest); err != nil {
		t.Fatal(err)
	}
	expected := errors.New("package preflight failed")
	preflightCalled, pushed := false, false
	provider := repositoryTestProvider{testPackageManager{
		id: "future",
		preflight: func(_ context.Context, _ string, version, task string) error {
			preflightCalled = true
			if version != "v1.3.0-rc.42" || task != "publish:rc" {
				t.Fatalf("wrong provider inputs %s %s", version, task)
			}
			return expected
		},
	}}
	useTestPackageManagers(t, provider)
	oldPush := pushReleaseTag
	pushReleaseTag = func(context.Context, string, string, transport.AuthMethod) error { pushed = true; return nil }
	t.Cleanup(func() { pushReleaseTag = oldPush })
	t.Setenv("GITHUB_TOKEN", "test-token")

	_, err := PublishRelease(t.Context(), root, ReleasePublishOptions{Channel: "rc", ExpectedVersion: "v1.3.0-rc.42"}, io.Discard, io.Discard)

	if !errors.Is(err, expected) || !preflightCalled || pushed {
		t.Fatalf("publication bypassed generic preflight: called=%v pushed=%v err=%v", preflightCalled, pushed, err)
	}
}

// Private helpers ------------------------------------------------------------

func (repositoryTestProvider) PublishesRepositoryPackages() bool { return true }
