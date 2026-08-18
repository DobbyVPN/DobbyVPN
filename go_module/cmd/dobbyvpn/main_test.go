package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go_module/grpcproto"
)

func TestReadSourceAcceptsInlineURLAndFileWithoutReadingURL(t *testing.T) {
	inline := "[[Outline]]\nServer='vpn.invalid'\n"
	got, err := readSource(inline)
	if err != nil || string(got) != inline {
		t.Fatalf("inline source = %q, %v", got, err)
	}
	urlSource := "HTTPS://example.invalid/config?token=synthetic"
	got, err = readSource(urlSource)
	if err != nil || string(got) != urlSource {
		t.Fatalf("URL source = %q, %v", got, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	file := []byte("[[Xray]]\noutbounds=[]\n")
	if err := os.WriteFile(path, file, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = readSource(path)
	if err != nil || string(got) != string(file) {
		t.Fatalf("file source = %q, %v", got, err)
	}
}

func TestReadSourceRejectsUnsupportedURLAndOversize(t *testing.T) {
	for _, source := range []string{"ftp://example.invalid/config", "file:///tmp/config"} {
		if _, err := readSource(source); err == nil {
			t.Fatalf("readSource(%q) unexpectedly succeeded", source)
		}
	}
	if _, err := readSource(strings.Repeat("x", maxSource+1)); err == nil {
		t.Fatal("oversize source unexpectedly succeeded")
	}
}

func TestReportFailureUsesConflictExitCodeOnlyForConflict(t *testing.T) {
	conflict := &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT}
	if got := reportFailure(nil, conflict); got != exitConflict {
		t.Fatalf("conflict exit=%d, want %d", got, exitConflict)
	}
	unsupported := &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED}
	if got := reportFailure(nil, unsupported); got != exitRuntime {
		t.Fatalf("unsupported exit=%d, want %d", got, exitRuntime)
	}
}
