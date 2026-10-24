package helper

import "testing"

// Tests ----------------------------------------------------------------------

func TestMessage(t *testing.T) {
	t.Run("when requesting a greeting", func(t *testing.T) {
		message := Message()

		t.Run("it returns hello", func(t *testing.T) {
			if message != "hello" {
				t.Fatalf("message = %q, want hello", message)
			}
		})
	})
}
