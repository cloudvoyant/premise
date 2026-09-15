package cmd

import (
	"context"
	"testing"
)

// TestResolveSelectorArgumentRoutesExplicitSelectorPassthrough verifies the
// command routes fully qualified selectors — explicit, local, and URL — straight
// through to generation without opening a picker or touching the network.
func TestResolveSelectorArgumentRoutesExplicitSelectorPassthrough(t *testing.T) {
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

// TestResolveSelectorArgumentSurfacesMalformedSelector verifies the command
// surfaces classification errors for malformed selectors rather than routing
// them to the scoped picker. The detailed classification cases live in core.
func TestResolveSelectorArgumentSurfacesMalformedSelector(t *testing.T) {
	if _, err := resolveSelectorArgument(context.Background(), []string{"cloudvoyant/premise:"}); err == nil {
		t.Fatal("resolveSelectorArgument succeeded, want error")
	}
}
