package miselibtemplate

import "testing"

func TestStartsWith(t *testing.T) {
	if !StartsWith("hello world", "hello") {
		t.Error(`StartsWith("hello world", "hello") = false, want true`)
	}
	if StartsWith("hello", "world") {
		t.Error(`StartsWith("hello", "world") = true, want false`)
	}
}

func TestEndsWith(t *testing.T) {
	if !EndsWith("hello world", "world") {
		t.Error(`EndsWith("hello world", "world") = false, want true`)
	}
	if EndsWith("hello", "world") {
		t.Error(`EndsWith("hello", "world") = true, want false`)
	}
}
