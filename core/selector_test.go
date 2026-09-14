package core

import (
	"strings"
	"testing"
)

func TestParseSelector(t *testing.T) {
	tests := []struct {
		selector string
		source   string
		name     string
		local    bool
	}{
		{selector: ":premise-app", source: NativeTemplateSource, name: "premise-app"},
		{selector: ":app", source: NativeTemplateSource, name: "app"},
		{selector: ".:lib", source: ".", name: "lib", local: true},
		{selector: "cloudvoyant/premise-template:app", source: "cloudvoyant/premise-template", name: "app"},
		{selector: "https://github.com/cloudvoyant/premise-template.git:lib", source: "https://github.com/cloudvoyant/premise-template.git", name: "lib"},
	}
	for _, test := range tests {
		selection, err := ParseSelector(test.selector)
		if err != nil {
			t.Fatalf("%s: %v", test.selector, err)
		}
		if selection.Source != test.source || selection.Name != test.name || selection.Local != test.local {
			t.Errorf("%s: unexpected selection %#v", test.selector, selection)
		}
	}
	for _, selector := range []string{"app", ":", ":../app"} {
		if _, err := ParseSelector(selector); err == nil {
			t.Errorf("expected %q to be rejected", selector)
		}
	}
}

func TestClassifyGenerateArgument(t *testing.T) {
	tests := []struct {
		name  string
		arg   string
		kind  SelectorKind
		value string
	}{
		{name: "empty argument selects the default picker", arg: "", kind: SelectorDefault},
		{name: "bare name shorthand", arg: ":premise-rust-lib", kind: SelectorOfficialName, value: "premise-rust-lib"},
		{name: "source-only scoped picker", arg: "cloudvoyant/premise-cargo", kind: SelectorSource, value: "cloudvoyant/premise-cargo"},
		{name: "explicit selector", arg: "cloudvoyant/premise:premise-app", kind: SelectorExplicit, value: "cloudvoyant/premise:premise-app"},
		{name: "local path", arg: ".:app", kind: SelectorExplicit, value: ".:app"},
		{name: "local parent path", arg: "../templates:lib", kind: SelectorExplicit, value: "../templates:lib"},
		{name: "URL selector", arg: "https://github.com/cloudvoyant/premise-template.git:app", kind: SelectorExplicit, value: "https://github.com/cloudvoyant/premise-template.git:app"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classified, err := ClassifyGenerateArgument(test.arg)
			if err != nil {
				t.Fatalf("ClassifyGenerateArgument(%q): %v", test.arg, err)
			}
			if classified.Kind != test.kind || classified.Value != test.value {
				t.Errorf("ClassifyGenerateArgument(%q) = %#v, want kind %v value %q", test.arg, classified, test.kind, test.value)
			}
		})
	}
}

func TestClassifyGenerateArgumentRejectsMalformedSelector(t *testing.T) {
	// A colon-carrying argument that fails to parse is a malformed selector,
	// not a registry source; it must surface the validation error instead of
	// being handed to the scoped picker (which would clone a bogus source).
	cases := []struct {
		selector string
		want     string
	}{
		{selector: ":", want: "invalid template name"},
		{selector: ":bad name", want: "invalid template name"},
		{selector: "cloudvoyant/premise:", want: "must end with :<template>"},
		{selector: "cloudvoyant/premise:bad name", want: "invalid template name"},
	}
	for _, test := range cases {
		_, err := ClassifyGenerateArgument(test.selector)
		if err == nil {
			t.Fatalf("ClassifyGenerateArgument(%q) succeeded, want error", test.selector)
		}
		if !strings.Contains(err.Error(), test.want) {
			t.Fatalf("ClassifyGenerateArgument(%q) error = %v, want containing %q", test.selector, err, test.want)
		}
	}
}
