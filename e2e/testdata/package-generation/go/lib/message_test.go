package helper

import "testing"

func TestMessage(t *testing.T) {
	if Message() != "hello" {
		t.Fatal("unexpected message")
	}
}
