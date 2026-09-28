package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestParseKernelMetadata(t *testing.T) {
	env := "CONTROLLER_ADDRESS=10.0.0.1:50052\nINSTANCE_ID=123\nFUNCTION_ID=77\nCUSTOM=value"
	cmdline := "console=ttyS0 hyperfaas.controller=ignored:1 hyperfaas.instance_id=1 hyperfaas.function_id=2 hyperfaas.env_b64=" + base64.RawURLEncoding.EncodeToString([]byte(env))
	path := filepath.Join(t.TempDir(), "cmdline")
	if err := os.WriteFile(path, []byte(cmdline), 0o600); err != nil {
		t.Fatalf("write cmdline: %v", err)
	}

	metadata, err := parseKernelMetadata(path)
	if err != nil {
		t.Fatalf("parseKernelMetadata: %v", err)
	}
	for key, want := range map[string]string{
		"CONTROLLER_ADDRESS": "10.0.0.1:50052",
		"INSTANCE_ID":        "123",
		"FUNCTION_ID":        "77",
		"CUSTOM":             "value",
	} {
		if got := metadata[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
