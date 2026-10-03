package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogCursorFollowsAppendTruncateAndReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.jsonl")
	var output bytes.Buffer
	cursor := logCursor{}
	if err := cursor.copyAvailable(&output, path); err != nil {
		t.Fatal(err)
	}
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := cursor.copyAvailable(&output, path); err != nil {
			t.Fatal(err)
		}
	}
	write("first\n")
	write("first\nsecond\n")
	write("new\n")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	write("replacement\n")
	for _, text := range []string{"first\n", "second\n", "new\n", "replacement\n"} {
		if strings.Count(output.String(), text) != 1 {
			t.Fatalf("expected exactly one %q in %q", text, output.String())
		}
	}
}

func TestDiagnosticsExportFreshCompleteAndNoOverwrite(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "backend.jsonl")
	destination := filepath.Join(directory, "export.txt")
	text := strings.Repeat("complete diagnostic line\n", 10000)
	if err := os.WriteFile(source, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exportDiagnostics(destination, []string{source}); err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(result), text) || !strings.Contains(string(result), "Platform:") {
		t.Fatal("incomplete export")
	}
	if err := exportDiagnostics(source, []string{source}); err == nil {
		t.Fatal("overwrote diagnostic input")
	}
	if err := exportDiagnostics(destination, []string{source}); err == nil {
		t.Fatal("overwrote existing export")
	}
}

func TestDiagnosticsCollectsOtherFilesAfterReadFailure(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "backend.jsonl")
	if err := os.WriteFile(source, []byte("available diagnostics"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := streamDiagnostics(context.Background(), &output, []string{directory, source}, false)
	if err == nil || !strings.Contains(err.Error(), directory) {
		t.Fatalf("missing collection failure: %v", err)
	}
	if !strings.Contains(output.String(), "available diagnostics") {
		t.Fatal("lost readable diagnostics after another file failed")
	}
}
