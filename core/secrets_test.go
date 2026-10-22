package core

import (
	"bytes"
	"testing"
)

// Public API -----------------------------------------------------------------

func TestSecretRedactionAcrossOutputWrites(t *testing.T) {
	var output bytes.Buffer
	writer := secretRedactor{destination: &output, values: map[string]string{"DATABASE_URL": "sensitive-value"}}
	_, _ = writer.Write([]byte("result: sensitive-"))
	_, _ = writer.Write([]byte("value\n"))
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "result: [REDACTED]\n" {
		t.Fatal("split output exposed a secret")
	}
}
