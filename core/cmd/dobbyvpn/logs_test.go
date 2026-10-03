package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogViewRetainsWritesAcrossRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.jsonl")
	var output bytes.Buffer
	view := &logView{}
	defer view.close()
	write := func(path, data string) {
		t.Helper()
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.WriteString(data)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("write=%v close=%v", err, closeErr)
		}
	}
	poll := func() {
		t.Helper()
		if err := view.copyAvailable(&output, []string{path}, nil); err != nil {
			t.Fatal(err)
		}
	}
	write(path, "first\n")
	poll()
	write(path, "second\n")
	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	write(path, "replacement\n")
	poll()
	poll()
	for _, text := range []string{"first\n", "second\n", "replacement\n"} {
		if strings.Count(output.String(), text) != 1 {
			t.Fatalf("expected exactly one %q in %q", text, output.String())
		}
	}
}

func TestClearPersistsViewBoundaryWithoutChangingExport(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "backend.jsonl")
	cli := filepath.Join(directory, "custom-cli.jsonl")
	t.Setenv("DOBBY_CLI_LOG_PATH", cli)
	if err := os.WriteFile(path, []byte("before clear\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveViewBoundary([]string{path}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cli + ".view"); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("after clear\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := streamDiagnostics(context.Background(), &output, []string{path}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "before clear") || !strings.Contains(output.String(), "after clear") || !strings.Contains(output.String(), "new generation") {
		t.Fatal(output.String())
	}
	destination := filepath.Join(directory, "export.gz")
	if err := exportDiagnostics(destination, []string{path}); err != nil {
		t.Fatal(err)
	}
	exported := readGzip(t, destination)
	for _, data := range []string{"before clear", "after clear", "new generation"} {
		if !strings.Contains(exported, data) {
			t.Fatalf("export lost %q", data)
		}
	}
}

func readGzip(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
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
	result := readGzip(t, destination)
	if !strings.HasSuffix(result, text) || !strings.Contains(result, "Build:") {
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
	t.Setenv("DOBBY_CLI_LOG_PATH", filepath.Join(t.TempDir(), "cli"))
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
