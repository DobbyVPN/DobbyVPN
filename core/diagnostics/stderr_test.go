package diagnostics

import (
	"bytes"
	"context"
	"fmt"
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
