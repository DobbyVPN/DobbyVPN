//go:build !(android || ios)

package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDefaultLogExportNameIsStableAndCompressed(t *testing.T) {
	got := defaultLogExportName(time.Date(2026, 9, 19, 12, 34, 56, 0, time.UTC))
	if got != "DobbyVPN_logs_2026-09-19_12-34-56.jsonl.gz" {
		t.Fatalf("name = %q", got)
	}
}

func TestWriteGzipPreservesDiagnosticLines(t *testing.T) {
	var archive bytes.Buffer
	if err := writeGzip(&archive, "warning\nfailure"); err != nil {
		t.Fatalf("write gzip: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if string(data) != strings.Join([]string{"warning", "failure"}, "\n") {
		t.Fatalf("payload = %q", data)
	}
}
