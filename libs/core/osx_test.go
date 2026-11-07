package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsRegularFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "go.mod")
	if err := os.WriteFile(file, []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
		want bool
	}{
		{name: "file", path: file, want: true},
		{name: "directory", path: root},
		{name: "missing", path: filepath.Join(root, "missing")},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := IsRegularFile(test.path)
			if err != nil || got != test.want {
				t.Fatalf("IsRegularFile(%q) = %v, %v; want %v", test.path, got, err, test.want)
			}
		})
	}
	link := filepath.Join(root, "linked.mod")
	if err := os.Symlink(file, link); err == nil {
		if got, err := IsRegularFile(link); err != nil || !got {
			t.Fatalf("IsRegularFile(symlink) = %v, %v; want true", got, err)
		}
	}
}
