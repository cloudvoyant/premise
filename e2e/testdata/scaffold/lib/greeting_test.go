package greeting

import "testing"

// Public API -----------------------------------------------------------------

func TestMessageNamesTheProject(t *testing.T) {
	if got := Message(); got != "premise-lib" {
		t.Fatalf("message = %q", got)
	}
}
