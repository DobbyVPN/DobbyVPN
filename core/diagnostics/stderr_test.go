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
		runStderrRestartChild(t, path)
		return
	}

	path := filepath.Join(t.TempDir(), "backend.stderr")
	writeRawClearFixture(t, path)
	before, captureErr := Capture([]string{path})
	if captureErr != nil {
		_ = before.Close()
		t.Fatal(captureErr)
	}
	if len(before.Inputs) != 1 {
		closeErr := before.Close()
		t.Fatalf("captured %d raw input generations before restart, want 1: %v", len(before.Inputs), closeErr)
	}
	originalID, clearOffset := before.Inputs[0].ID, before.Inputs[0].Size
	if closeErr := before.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	runStderrRestartChildProcess(t, path)
	assertStderrClearView(t, path, originalID, clearOffset)
}

func runStderrRestartChild(t *testing.T, path string) {
	t.Helper()
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
}

func writeRawClearFixture(t *testing.T, path string) {
	t.Helper()
	first := []byte("pre-clear raw first\n")
	tail := append(append([]byte(nil), first...), []byte("pre-clear raw second\n")...)
	prefixSize := Threshold - int64(len(first)) + 2
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if truncateErr := file.Truncate(prefixSize); truncateErr != nil {
		_ = file.Close()
		t.Fatal(truncateErr)
	}
	if _, separatorErr := file.WriteAt([]byte("\n"), prefixSize-1); separatorErr != nil {
		_ = file.Close()
		t.Fatal(separatorErr)
	}
	if _, tailErr := file.WriteAt(tail, prefixSize); tailErr != nil {
		_ = file.Close()
		t.Fatal(tailErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func runStderrRestartChildProcess(t *testing.T, path string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestStderrRestartKeepsClearBoundaryAcrossRawLines$")
	child.Env = append(os.Environ(), "DOBBY_TEST_STDERR_RESTART_CHILD="+path)
	output, childErr := runStderrChild(t, child)
	if len(output) > 0 {
		t.Logf("%s", output)
	}
	if childErr != nil {
		t.Fatalf("stderr restart child: %v", childErr)
	}
}

func assertStderrClearView(t *testing.T, path, originalID string, clearOffset int64) {
	t.Helper()
	after, captureErr := Capture([]string{path})
	if captureErr != nil {
		_ = after.Close()
		t.Fatal(captureErr)
	}
	defer func() {
		if closeErr := after.Close(); closeErr != nil {
			t.Error(closeErr)
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
		start := clearOffset
		if input.ID != originalID {
			start = input.Size - 4096
			if start < 0 {
				start = 0
			}
		}
		if input.Size-start > 4096 {
			start = input.Size - 4096
		}
		chunk := make([]byte, int(input.Size-start))
		if _, readErr := input.File.ReadAt(chunk, start); readErr != nil && readErr != io.EOF {
			t.Fatal(readErr)
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
