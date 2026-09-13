package cmd

import (
	"context"
	"strings"
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

func TestResolveSelectorArgumentRejectsMalformedSelector(t *testing.T) {
	// A colon-carrying argument that fails to parse is a malformed selector,
	// not a registry source; it must surface the validation error instead of
	// being handed to the scoped picker (which would clone a bogus source).
	cases := []struct {
		selector string
		want     string
	}{
		{selector: "cloudvoyant/premise:", want: "must end with :<template>"},
		{selector: "cloudvoyant/premise:bad name", want: "invalid template name"},
	}
	for _, test := range cases {
		_, err := resolveSelectorArgument(context.Background(), []string{test.selector})
		if err == nil {
			t.Fatalf("resolveSelectorArgument(%q) succeeded, want error", test.selector)
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Fatalf("resolveSelectorArgument(%q) error = %v, want containing %q", test.selector, err, test.want)
		}
	}
}
