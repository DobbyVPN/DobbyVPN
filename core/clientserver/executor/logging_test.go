//go:build !(android || ios)

package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"core/diagnostics"
)

func TestStructuredStartupFailureSurvivesInRawStderr(t *testing.T) {
	if path := os.Getenv("DOBBY_LOGGER_FAILURE_CHILD"); path != "" {
		if err := initExplicitLocalLog(); err != nil {
			fmt.Fprintf(diagnostics.Stderr, "structured startup failed: %v\n", err)
			panic("backend cannot start")
		}
		os.Exit(0)
	}
	root := t.TempDir()
	path := filepath.Join(root, "backend.jsonl")
	// A directory at the structured lock path fails that sink, while its
	// separate stderr path remains writable.
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestStructuredStartupFailureSurvivesInRawStderr$")
	child.Env = append(os.Environ(), "DOBBY_LOGGER_FAILURE_CHILD="+path, "DOBBY_LOG_ROOT="+root, "DOBBY_LOG_PATH="+path, "DOBBY_LOG_PRECREATED=0")
	output, err := child.CombinedOutput()
	if len(output) > 0 {
		t.Logf("%s", output)
	}
	if err == nil {
		t.Fatal("invalid structured sink was accepted")
	}
	raw, err := os.ReadFile(path + ".stderr")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"stderr.capture", "structured startup failed:", "backend.jsonl.lock", "panic: backend cannot start"} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("raw capture lost %q: %s", want, raw)
		}
	}
}
