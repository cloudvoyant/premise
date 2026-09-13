package cmd

import (
	"context"
	"testing"
)

func TestResolveSelectorArgumentPassthrough(t *testing.T) {
	// Explicit, local, and URL selectors pass through unchanged and never open
	// a picker or touch the network.
	cases := []string{
		"cloudvoyant/premise-cargo:premise-rust-lib",
		"cloudvoyant/premise:premise-app",
		".:app",
		"../templates:lib",
		"https://github.com/cloudvoyant/premise-template.git:app",
	}
	for _, selector := range cases {
		got, err := resolveSelectorArgument(context.Background(), []string{selector})
		if err != nil {
			t.Fatalf("resolveSelectorArgument(%q): %v", selector, err)
		}
		if got != selector {
			t.Fatalf("resolveSelectorArgument(%q) = %q, want passthrough", selector, got)
		}
	}
}
