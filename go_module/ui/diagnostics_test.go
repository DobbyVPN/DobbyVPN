package ui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

func diagnosticEvent(t *testing.T, timestamp, source, event, message string) string {
	t.Helper()
	value, err := json.Marshal(map[string]string{
		"schema":    logSchema,
		"timestamp": timestamp,
		"level":     "INFO",
		"source":    source,
		"event":     event,
		"message":   message,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(value) + "\n"
}

func writeDiagnosticFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFileDiagnosticStoreMergesAndBoundsHistory(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "app_logs.txt")
	service := filepath.Join(root, "go_desktop_service_logs.jsonl")
	old := "2026-09-19T10:00:00.000Z"
	newer := "2026-09-19T10:00:01.000Z"
	contents := strings.Repeat("legacy diagnostic\n", maxDiagnosticTail+3)
	contents += diagnosticEvent(t, newer, "go", "status.snapshot", "new-service-event")
	writeDiagnosticFile(t, primary, contents)
	writeDiagnosticFile(t, service, diagnosticEvent(t, old, "service", "status.snapshot", "old-service-event"))

	store := newFileDiagnosticStore(primary, service)
	history, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if len(history.UILines) != maxDiagnosticTail {
		t.Fatalf("UI tail length = %d, want %d", len(history.UILines), maxDiagnosticTail)
	}
	if len(history.ExportLines) != maxDiagnosticTail+5 {
		t.Fatalf("export line count = %d, want %d", len(history.ExportLines), maxDiagnosticTail+5)
	}
	if !strings.Contains(strings.Join(history.UILines, "\n"), "new-service-event") {
		t.Fatalf("UI tail omitted newest service event: %q", history.UILines)
	}
	if !strings.Contains(strings.Join(history.UILines, "\n"), "old-service-event") {
		t.Fatalf("UI tail omitted retained older service event: %q", history.UILines)
	}
}

func TestFileDiagnosticStoreClearKeepsProducerAndHidesOlderRecords(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "app_logs.txt")
	service := filepath.Join(root, "go_desktop_service_logs.jsonl")
	writeDiagnosticFile(t, primary, diagnosticEvent(t, "2026-09-19T09:00:00.000Z", "app", "status.snapshot", "before-clear"))
	writeDiagnosticFile(t, service, diagnosticEvent(t, "2026-09-19T09:00:00.000Z", "service", "status.snapshot", "producer-before-clear"))

	store := newFileDiagnosticStore(primary, service)
	if err := store.Clear(context.Background()); err != nil {
		t.Fatalf("clear history: %v", err)
	}
	// Simulate a producer that remains alive while the UI clears its own file.
	serviceAfter := diagnosticEvent(t, "2026-09-19T09:00:00.000Z", "service", "status.snapshot", "producer-before-clear") +
		diagnosticEvent(t, "2999-09-19T09:00:00.000Z", "service", "status.snapshot", "producer-after-clear")
	writeDiagnosticFile(t, service, serviceAfter)

	history, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	raw := strings.Join(history.ExportLines, "\n")
	if len(history.UILines) != 1 || !strings.Contains(history.UILines[0], "producer-after-clear") {
		t.Fatalf("visible history after clear = %q, want only newer producer event", history.UILines)
	}
	if strings.Contains(raw, "before-clear") || strings.Contains(raw, "producer-before-clear") {
		t.Fatalf("clear retained an older record: %s", raw)
	}
	if !strings.Contains(raw, "Earlier diagnostic events were cleared from this view") {
		t.Fatalf("clear marker missing from retained export: %s", raw)
	}
	if !strings.Contains(raw, "producer-after-clear") {
		t.Fatalf("new producer record missing after clear: %s", raw)
	}
	producerRaw, err := os.ReadFile(service)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(producerRaw), "producer-before-clear") {
		t.Fatal("clear modified the producer-owned file")
	}
}

func TestFileDiagnosticStoreClearHidesLegacyProducerLines(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "ui_diagnostics.jsonl")
	producer := filepath.Join(root, "native_logs.jsonl")
	writeDiagnosticFile(t, producer, "legacy native line\n")
	store := newFileDiagnosticStore(primary, producer)
	if err := store.Clear(context.Background()); err != nil {
		t.Fatalf("clear history: %v", err)
	}
	history, err := store.Read(context.Background())
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if len(history.UILines) != 0 {
		t.Fatalf("legacy producer history remained visible after clear: %q", history.UILines)
	}
	contents, err := os.ReadFile(producer)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "legacy native line\n" {
		t.Fatalf("clear modified legacy producer file: %q", contents)
	}
}

func TestFileDiagnosticStoreClearOrdersMixedRFC3339PrecisionChronologically(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "ui_diagnostics.jsonl")
	producer := filepath.Join(root, "native_logs.jsonl")
	writeDiagnosticFile(t, primary, diagnosticEvent(
		t, "2026-09-19T09:00:05.123Z", "go-ui", "logs.cleared", "clear",
	))
	writeDiagnosticFile(t, producer,
		diagnosticEvent(t, "2026-09-19T09:00:05Z", "ios-native", "log.message", "before-same-second")+
			diagnosticEvent(t, "2026-09-19T09:00:05.124000Z", "android-native", "log.message", "after-same-second"),
	)

	history, err := newFileDiagnosticStore(primary, producer).Read(context.Background())
	if err != nil {
		t.Fatalf("read mixed-precision history: %v", err)
	}
	raw := strings.Join(history.ExportLines, "\n")
	if strings.Contains(raw, "before-same-second") {
		t.Fatalf("pre-clear whole-second record leaked after fractional marker: %s", raw)
	}
	if !strings.Contains(raw, "after-same-second") {
		t.Fatalf("post-clear mixed-precision record is missing: %s", raw)
	}
}

func TestFileDiagnosticStoreClearRejectsSymlink(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("symlink creation may require elevated Windows privileges")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.log")
	primary := filepath.Join(root, "app_logs.txt")
	writeDiagnosticFile(t, target, "keep-target\n")
	if err := os.Symlink(target, primary); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := newFileDiagnosticStore(primary)
	if err := store.Clear(context.Background()); err == nil {
		t.Fatal("clear followed a diagnostic symlink")
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "keep-target\n" {
		t.Fatalf("symlink target changed: %q", contents)
	}
}

type fakeDiagnosticStore struct {
	history  DiagnosticHistory
	readErr  error
	clearErr error
	cleared  bool
}

type blockingDiagnosticStore struct {
	mu      sync.Mutex
	active  int
	maximum int
	entered chan struct{}
	release chan struct{}
}

type mutableDiagnosticStore struct {
	mu      sync.Mutex
	history DiagnosticHistory
}

func (s *mutableDiagnosticStore) Read(context.Context) (DiagnosticHistory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DiagnosticHistory{
		UILines:     append([]string(nil), s.history.UILines...),
		ExportLines: append([]string(nil), s.history.ExportLines...),
	}, nil
}

func (s *mutableDiagnosticStore) Clear(context.Context) error { return nil }

func (s *mutableDiagnosticStore) setHistory(history DiagnosticHistory) {
	s.mu.Lock()
	s.history = history
	s.mu.Unlock()
}

// stalledSessionClient keeps the session watcher out of the way while a
// diagnostic watcher test exercises its own lifecycle. The real client can
// be unavailable during startup too, but it must not make this test depend on
// a second Fyne presentation goroutine.
type stalledSessionClient struct{}

func (stalledSessionClient) Configure(context.Context, []byte, uint64) (ConfigureResult, error) {
	return ConfigureResult{}, nil
}

func (stalledSessionClient) Start(context.Context, uint64) (StartResult, error) {
	return StartResult{}, nil
}

func (stalledSessionClient) Stop(context.Context, uint64) (StopResult, error) {
	return StopResult{}, nil
}

func (stalledSessionClient) Snapshot(ctx context.Context) (Snapshot, error) {
	<-ctx.Done()
	return Snapshot{}, ctx.Err()
}

func (stalledSessionClient) Watch(ctx context.Context) (<-chan Snapshot, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (stalledSessionClient) Reset(context.Context, uint64) (Snapshot, error) {
	return Snapshot{}, nil
}

func (s *blockingDiagnosticStore) Read(context.Context) (DiagnosticHistory, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maximum {
		s.maximum = s.active
	}
	select {
	case s.entered <- struct{}{}:
	default:
	}
	s.mu.Unlock()
	<-s.release
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return DiagnosticHistory{}, nil
}

func (s *blockingDiagnosticStore) Clear(context.Context) error { return nil }

func (s *fakeDiagnosticStore) Read(context.Context) (DiagnosticHistory, error) {
	if s.readErr != nil {
		return DiagnosticHistory{}, s.readErr
	}
	return DiagnosticHistory{
		UILines:     append([]string(nil), s.history.UILines...),
		ExportLines: append([]string(nil), s.history.ExportLines...),
	}, nil
}

func (s *fakeDiagnosticStore) Clear(context.Context) error {
	if s.clearErr != nil {
		return s.clearErr
	}
	s.cleared = true
	s.history = DiagnosticHistory{}
	return nil
}

func TestConnectionViewUsesRetainedHistoryForDisplayAndExport(t *testing.T) {
	store := &fakeDiagnosticStore{history: DiagnosticHistory{
		UILines:     []string{"[INFO] retained history"},
		ExportLines: []string{"{\"event\":\"status.snapshot\",\"message\":\"retained history\"}"},
	}}
	exporter := &fakeLogExporter{}
	view := NewConnectionViewWithLogExporterAndDiagnostics(nil, exporter, store)
	view.render(Snapshot{State: StateFailed, LastFailure: &Failure{Code: "CURRENT", Message: "snapshot fallback"}})
	view.refreshDiagnostics(context.Background())
	if view.Logs.Text != "[INFO] retained history" {
		t.Fatalf("logs = %q, want retained history", view.Logs.Text)
	}
	view.Export.OnTapped()
	got := exporter.captured()
	if len(got) != 1 || got[0] != store.history.ExportLines[0] {
		t.Fatalf("exported lines = %q, want retained raw history", got)
	}
	view.ClearLogs.OnTapped()
	if !store.cleared {
		t.Fatal("clear action did not call diagnostic store")
	}
	if view.Logs.Text != "" {
		t.Fatalf("logs after successful clear = %q, want empty", view.Logs.Text)
	}
}

func TestConnectionViewPreservesHistoryWhenClearFails(t *testing.T) {
	store := &fakeDiagnosticStore{
		history:  DiagnosticHistory{UILines: []string{"retained"}, ExportLines: []string{"raw-retained"}},
		clearErr: errors.New("permission denied"),
	}
	view := NewConnectionViewWithDiagnostics(nil, store)
	view.refreshDiagnostics(context.Background())
	view.ClearLogs.OnTapped()
	if view.Logs.Text != "retained" {
		t.Fatalf("failed clear erased visible history: %q", view.Logs.Text)
	}
	if !strings.Contains(view.LogStatus.Text, "LOCAL_LOG_STORAGE_UNAVAILABLE") {
		t.Fatalf("failed clear status = %q", view.LogStatus.Text)
	}
}

func TestConnectionViewSerializesDiagnosticReads(t *testing.T) {
	store := &blockingDiagnosticStore{entered: make(chan struct{}, 2), release: make(chan struct{})}
	view := NewConnectionViewWithDiagnostics(nil, store)
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	go func() {
		view.refreshDiagnostics(context.Background())
		close(firstDone)
	}()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("first diagnostic read did not start")
	}
	go func() {
		view.refreshDiagnostics(context.Background())
		close(secondDone)
	}()
	time.Sleep(20 * time.Millisecond)
	store.mu.Lock()
	maximum := store.maximum
	active := store.active
	store.mu.Unlock()
	if maximum != 1 || active != 1 {
		t.Fatalf("diagnostic reads overlapped: maximum=%d active=%d", maximum, active)
	}
	close(store.release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first diagnostic read did not finish")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second diagnostic read did not finish")
	}
}

func TestConnectionViewDiagnosticWatcherPicksUpDeferredStore(t *testing.T) {
	runtime := test.NewApp()
	t.Cleanup(runtime.Quit)
	view := NewConnectionViewWithDiagnostics(&stalledSessionClient{}, nil)
	view.Start()
	t.Cleanup(view.Stop)

	store := &mutableDiagnosticStore{history: DiagnosticHistory{
		UILines:     []string{"initial diagnostic"},
		ExportLines: []string{"initial raw diagnostic"},
	}}
	// Simulate the platform resolver's synchronized field injection after
	// Start. Do not call SetDiagnosticStore here: that method intentionally
	// performs an immediate UI refresh, while this test specifically proves
	// that the already-running 500 ms watcher also discovers the late store.
	view.mu.Lock()
	view.diagnostics = store
	view.mu.Unlock()
	waitForRenderedDiagnostics(t, view, "initial diagnostic")

	store.setHistory(DiagnosticHistory{
		UILines:     []string{"deferred diagnostic"},
		ExportLines: []string{"deferred raw diagnostic"},
	})
	waitForRenderedDiagnostics(t, view, "deferred diagnostic")
}

func waitForRenderedDiagnostics(t *testing.T, view *ConnectionView, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view.mu.Lock()
		got := view.renderedLogs
		view.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	view.mu.Lock()
	got := view.renderedLogs
	view.mu.Unlock()
	t.Fatalf("rendered diagnostics = %q, want %q", got, want)
}

func TestDiagnosticHistoryReadHonorsContext(t *testing.T) {
	store := newFileDiagnosticStore(filepath.Join(t.TempDir(), "app_logs.txt"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read error = %v, want context canceled", err)
	}
	if err := store.Clear(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("clear error = %v, want context canceled", err)
	}
}
