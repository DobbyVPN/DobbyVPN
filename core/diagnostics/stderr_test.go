package diagnostics

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStderrRetainsOvershootRotationAndPanic(t *testing.T) {
	if path := os.Getenv("DOBBY_TEST_STDERR_CHILD"); path != "" {
		if err := CaptureStderr(path, ""); err != nil {
			panic(err)
		}
		checkStderrOwnership(t)
		stderrCapture.mu.Lock()
		stderrCapture.limit = 1024
		stderrCapture.mu.Unlock()
		fmt.Fprintln(Stderr, strings.Repeat("before-rotation", 100))
		if err := stderrCapture.rotate(); err != nil {
			panic(err)
		}
		checkStderrOwnership(t)
		fmt.Fprintln(Stderr, "after-rotation")
		if err := writeNativeStderr([]byte("native descriptor after rotation\n")); err != nil {
			panic(err)
		}
		panic("original panic cause")
	}
	path := filepath.Join(t.TempDir(), "raw.stderr")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestStderrRetainsOvershootRotationAndPanic$")
	child.Env = append(os.Environ(), "DOBBY_TEST_STDERR_CHILD="+path)
	output, err := runStderrChild(t, child)
	// Preserve the child's complete output even when its panic is intentionally
	// captured by the product instead of the test process's pipes.
	if len(output) > 0 {
		t.Logf("%s", output)
	}
	if err == nil {
		t.Fatal("panic child succeeded")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path + PreviousSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(previous, []byte(strings.Repeat("before-rotation", 100)+"\n")) {
		t.Fatalf("incomplete oversized write: %s", previous)
	}
	for _, want := range []string{`"capture_generation":2`, `"run_id":`, "native descriptor after rotation", "after-rotation", "panic: original panic cause", "stderr_test.go"} {
		if !bytes.Contains(current, []byte(want)) {
			t.Fatalf("raw current lost %q: %s", want, current)
		}
	}
	if count := bytes.Count(current, []byte("panic: original panic cause")); count != 1 {
		t.Fatalf("panic cause written %d times: %s", count, current)
	}
}

func TestStderrRestartKeepsClearBoundaryAcrossRawLines(t *testing.T) {
	if path := os.Getenv("DOBBY_TEST_STDERR_RESTART_CHILD"); path != "" {
		if err := CaptureStderr(path, ""); err != nil {
			panic(err)
		}
		checkStderrOwnership(t)
		if err := stderrCapture.rotate(); err != nil {
			panic(err)
		}
		checkStderrOwnership(t)
		if _, err := fmt.Fprintln(Stderr, "post-clear raw marker"); err != nil {
			panic(err)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "backend.stderr")
	first := []byte("pre-clear raw first\n")
	tail := append(append([]byte(nil), first...), []byte("pre-clear raw second\n")...)
	prefixSize := Threshold - int64(len(first)) + 2
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(prefixSize); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("\n"), prefixSize-1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := file.WriteAt(tail, prefixSize); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	before, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Inputs) != 1 {
		t.Fatalf("captured %d raw input generations before restart, want 1", len(before.Inputs))
	}
	originalID := before.Inputs[0].ID
	clearOffsets := map[string]int64{originalID: before.Inputs[0].Size}
	if err := before.Close(); err != nil {
		t.Fatal(err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestStderrRestartKeepsClearBoundaryAcrossRawLines$")
	child.Env = append(os.Environ(), "DOBBY_TEST_STDERR_RESTART_CHILD="+path)
	output, err := runStderrChild(t, child)
	if len(output) > 0 {
		t.Logf("%s", output)
	}
	if err != nil {
		t.Fatalf("stderr restart child: %v", err)
	}

	after, err := Capture([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := after.Close(); err != nil {
			t.Error(err)
		}
	}()
	var visible bytes.Buffer
	foundOriginalPrevious := false
	for _, input := range after.Inputs {
		if filepath.Clean(input.Path) == filepath.Clean(path+PreviousSuffix) {
			foundOriginalPrevious = input.ID == originalID
			if !foundOriginalPrevious {
				t.Errorf("raw rotation changed the cleared generation identity: before=%s after=%s", originalID, input.ID)
			}
		}
		start, ok := clearOffsets[input.ID]
		if !ok {
			start = input.Size - 4096
			if start < 0 {
				start = 0
			}
		}
		if input.Size-start > 4096 {
			start = input.Size - 4096
		}
		chunk := make([]byte, input.Size-start)
		if _, err := input.File.ReadAt(chunk, start); err != nil && err != io.EOF {
			t.Fatal(err)
		}
		_, _ = visible.Write(chunk)
	}
	if !foundOriginalPrevious {
		t.Errorf("captured clear-boundary identity %s was not retained as .previous", originalID)
	}
	for _, old := range [][]byte{[]byte("pre-clear raw first"), []byte("pre-clear raw second")} {
		if bytes.Contains(visible.Bytes(), old) {
			t.Errorf("raw view after restart exposed cleared record %q", old)
		}
	}
	if !bytes.Contains(visible.Bytes(), []byte("post-clear raw marker")) {
		t.Errorf("raw view after restart lost post-clear data: %q", visible.Bytes())
	}
}
