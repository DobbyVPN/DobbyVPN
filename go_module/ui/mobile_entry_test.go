//go:build android || ios

package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	applicationlog "go_module/log"
)

func TestMobileProductionEntryPointInjectsLogExporter(t *testing.T) {
	if NewMobileLogExporter() == nil {
		t.Fatal("mobile production entrypoint has no log exporter")
	}
}

func TestMobileDiagnosticPathContractRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "ui_diagnostics.jsonl"),
		filepath.Join(root, "native_logs.jsonl"),
		filepath.Join(root, "go_app_logs.jsonl"),
	}
	paths[1] = filepath.Join(filepath.Dir(root), "outside.jsonl")
	if store := newMobileDiagnosticStore(paths); store == nil {
		t.Fatal("path validation returned a nil store")
	} else if _, err := store.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "escape") {
		t.Fatalf("escape error = %v, want an explicit path confinement failure", err)
	}
}

func TestMobileDiagnosticStoreUsesNativeMarkerAndPreservesProducer(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "ui_diagnostics.jsonl"),
		filepath.Join(root, "native_logs.jsonl"),
		filepath.Join(root, "go_app_logs.jsonl"),
	}
	if err := os.WriteFile(paths[1], []byte(`{"schema":"dobby.log/v1","timestamp":"2999-01-01T00:00:00Z","message":"native"}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newMobileDiagnosticStore(paths)
	history, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read native history: %v", err)
	}
	if len(history.UILines) != 1 || !strings.Contains(history.UILines[0], "native") {
		t.Fatalf("native history = %q", history.UILines)
	}
	if err := store.Clear(context.Background()); err != nil {
		t.Fatalf("clear mobile history: %v", err)
	}
	producer, err := os.ReadFile(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(producer), "native") {
		t.Fatal("clear modified a producer-owned file")
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("clear marker file missing: %v", err)
	}
	_ = applicationlog.Close()
}
