package diagnostics

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func writeRecord(t *testing.T, writer *Writer, value string) {
	t.Helper()
	if n, err := writer.Write([]byte(value)); err != nil || n != len(value) {
		t.Fatalf("write n=%d error=%v", n, err)
	}
}

func TestWholeRecordsRotateAndReplacePrevious(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.jsonl")
	writer, err := openWriter(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, writer, "12345678\n")
	writeRecord(t, writer, "abcdefgh\n") // Crossing record is retained in full.
	if got := readFile(t, path); got != "12345678\nabcdefgh\n" {
		t.Fatal(got)
	}
	writeRecord(t, writer, "third-record\n")
	if got := readFile(t, path+PreviousSuffix); got != "12345678\nabcdefgh\n" {
		t.Fatal(got)
	}
	writeRecord(t, writer, "fourth\n")
	if got := readFile(t, path+PreviousSuffix); got != "third-record\n" {
		t.Fatal(got)
	}
	if got := readFile(t, path); got != "fourth\n" {
		t.Fatal(got)
	}
}

func TestReopenKeepsClearBoundaryAcrossWholeRecordOverflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.jsonl")
	const limit int64 = 32
	anchor := "pre-clear anchor crosses threshold\n"
	if int64(len(anchor)) <= limit {
		t.Fatal("test anchor must cross the threshold")
	}
	writer, err := openWriter(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, writer, anchor)
	writeRecord(t, writer, "current\n") // Rotates the complete crossing record to .previous.
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	before, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	boundary := make(map[string]int64, len(before.Inputs))
	ids := make(map[string]string, len(before.Inputs))
	for _, input := range before.Inputs {
		boundary[input.ID] = input.Size
		ids[filepath.Base(input.Path)] = input.ID
	}
	if closeErr := before.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	// Reopening a writer is the startup path that invokes migrateHistory.
	restarted, err := openWriter(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, restarted, "post-clear\n")
	defer func() {
		if closeErr := restarted.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()

	after, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := after.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	var visible bytes.Buffer
	for _, input := range after.Inputs {
		if previousID, ok := ids[filepath.Base(input.Path)]; ok && previousID != input.ID {
			t.Errorf("startup migration replaced %s identity: before=%s after=%s", filepath.Base(input.Path), previousID, input.ID)
		}
		offset := boundary[input.ID]
		if _, copyErr := input.CopyTo(&visible, offset); copyErr != nil {
			t.Fatal(copyErr)
		}
	}
	if got := visible.String(); got != "post-clear\n" {
		t.Errorf("clear view exposed prior records or lost the new record: %q", got)
	}

	assertExportContains(t, path, anchor, "current\n", "post-clear\n")
}

func assertExportContains(t *testing.T, path string, retained ...string) {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "logs.gz")
	if exportErr := ExportGzip(destination, []string{path}, "header\n"); exportErr != nil {
		t.Fatal(exportErr)
	}
	archive, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		_ = archive.Close()
		t.Fatal(err)
	}
	exported, readErr := io.ReadAll(compressed)
	closeErr := errors.Join(compressed.Close(), archive.Close())
	if finalErr := errors.Join(readErr, closeErr); finalErr != nil {
		t.Fatal(finalErr)
	}
	for _, marker := range retained {
		if !bytes.Contains(exported, []byte(marker)) {
			t.Errorf("raw export lost retained record %q", marker)
		}
	}
}

func TestMigrationDetectsNewlineAtThresholdBeforeFileEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.jsonl")
	const limit int64 = 8
	if err := os.WriteFile(path, []byte("1234567\nlegacy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writer, err := openWriter(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	}()
	if got := readFile(t, path+PreviousSuffix); got != "1234567\n" {
		t.Fatalf("record ending at threshold was not migrated: %q", got)
	}
	if got := readFile(t, path); got != "legacy\n" {
		t.Fatalf("record after exact-threshold newline was not retained: %q", got)
	}
}

func TestOpenWriterPreservesIdentityForUnterminatedOverflowTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.jsonl")
	const limit int64 = 8
	content := []byte("unterminated crossing tail")
	if int64(len(content)) <= limit {
		t.Fatal("test tail must cross the threshold")
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Inputs) != 1 {
		t.Fatalf("captured %d inputs, want 1", len(before.Inputs))
	}
	identity := before.Inputs[0].ID
	if closeErr := before.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	writer, err := openWriter(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := writer.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	after, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := after.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if len(after.Inputs) != 1 || after.Inputs[0].ID != identity {
		t.Fatalf("startup migration changed unterminated-tail identity: before=%s after=%v", identity, after.Inputs)
	}
	if got := readFile(t, path); string(content) != got {
		t.Fatalf("startup migration changed unterminated tail: got %q", got)
	}
}

func TestOversizedHistoryMigrationStreamsLongRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.jsonl")
	long := strings.Repeat("a", 200_000) + "\n"
	if err := os.WriteFile(path, []byte("old\n"+long+"last\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	writer, err := openWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path+PreviousSuffix); got != "old\n"+long {
		t.Fatalf("migration split or lost long record: length=%d", len(got))
	}
	if got := readFile(t, path); got != "last\n" {
		t.Fatal(got)
	}
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	restarted, err := openWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, restarted, "restart\n")
	if got := readFile(t, path); got != "last\nrestart\n" {
		t.Fatal(got)
	}
}

func TestCapturedHandlesSurviveMultipleConcurrentRotations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.jsonl")
	writer, err := openWriter(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, writer, "previous-generation\n")
	writeRecord(t, writer, "current-generation\n")
	snapshot, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if _, err := fmt.Fprintf(writer, "new-record-%d\n", i); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	<-done
	var output bytes.Buffer
	for _, input := range snapshot.Inputs {
		if _, err := input.CopyTo(&output, 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := output.String(); got != "previous-generation\ncurrent-generation\n" {
		t.Fatalf("captured bytes changed: %q", got)
	}
}

func TestConcurrentWritersKeepRecordsWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.jsonl")
	writers := make([]*Writer, 8)
	for i := range writers {
		writer, err := openWriter(path, 1000)
		if err != nil {
			t.Fatal(err)
		}
		writers[i] = writer
	}
	var group sync.WaitGroup
	for i, writer := range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 100; j++ {
				if _, err := fmt.Fprintf(writer, "writer-%d-record-%03d\n", i, j); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	group.Wait()
	snapshot, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, input := range snapshot.Inputs {
		var output bytes.Buffer
		if _, err := input.CopyTo(&output, 0); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
			var writer, record int
			if _, err := fmt.Sscanf(line, "writer-%d-record-%03d", &writer, &record); err != nil {
				t.Fatalf("partial record %q: %v", line, err)
			}
		}
	}
}

func TestGzipExportIncludesBothGenerationsAndProtectsExistingDestination(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "app.jsonl")
	writer, err := openWriter(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	writeRecord(t, writer, "retained-previous\n")
	writeRecord(t, writer, "retained-current\n")
	destination := filepath.Join(directory, "logs.gz")
	if exportErr := ExportGzip(destination, []string{path}, "build metadata\n"); exportErr != nil {
		t.Fatal(exportErr)
	}
	input, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	reader, err := gzip.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"build metadata", "retained-previous", "retained-current"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("export lost %q", want)
		}
	}
	before := readFile(t, destination)
	if err := ExportGzip(destination, []string{path}, "replacement"); err == nil {
		t.Fatal("overwrote existing destination")
	}
	if got := readFile(t, destination); got != before {
		t.Fatal("existing destination changed")
	}
}

func TestExportReportsInvalidInputWithoutCreatingDestination(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "logs.gz")
	if err := ExportGzip(destination, []string{directory}, "header"); err == nil {
		t.Fatal("directory input accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("failed destination retained: %v", err)
	}
}
