package diagnostics

import (
	"bytes"
	"compress/gzip"
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
