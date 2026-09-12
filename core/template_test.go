package core

import "testing"

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
