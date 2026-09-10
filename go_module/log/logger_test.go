package log

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSetPathCreatesLogAndFlushesBufferedEntries(t *testing.T) {
	initMu.Lock()
	previous := lg
	lg = &Logger{}
	initMu.Unlock()
	defer func() {
		initMu.Lock()
		if lg.file != nil {
			_ = lg.file.Close()
		}
		lg = previous
		initMu.Unlock()
	}()

	writeEventAt(
		time.Time{},
		slog.LevelInfo,
		"status.snapshot",
		"SESSION",
		"startup status=idle",
		map[string]any{"token": "startup-secret"},
	)
	path := filepath.Join(t.TempDir(), "private", "dobby.log")
	if err := SetPath(path); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(output, &event); err != nil {
		t.Fatalf("buffered event is not JSON: %v: %s", err, output)
	}
	for key, want := range map[string]string{
		"schema": "dobby.log/v1", "source": "go", "event": "status.snapshot", "category": "SESSION",
	} {
		if got := event[key]; got != want {
			t.Fatalf("%s = %v, want %q", key, got, want)
		}
	}
	if !strings.Contains(string(output), "startup-secret") || !strings.Contains(string(output), "startup status=idle") {
		t.Fatalf("buffered event lost complete diagnostics: %s", output)
	}
	if timestamp, _ := event["timestamp"].(string); strings.HasPrefix(timestamp, "0001-") || timestamp == "" {
		t.Fatalf("buffered event has invalid fallback timestamp: %#v", event)
	}
}

func TestSetOpenedFilePreservesSupervisorPermissions(t *testing.T) {
	initMu.Lock()
	previous := lg
	lg = &Logger{}
	initMu.Unlock()
	defer func() {
		initMu.Lock()
		if lg.file != nil {
			_ = lg.file.Close()
		}
		lg = previous
		initMu.Unlock()
	}()

	path := filepath.Join(t.TempDir(), "managed.log")
	if err := os.WriteFile(path, []byte("prefix\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetOpenedFile(file); err != nil {
		t.Fatal(err)
	}
	Info("MANAGED", "retained", nil)
	if err := lg.file.Sync(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("managed log mode = %o, want unchanged 640", info.Mode().Perm())
	}
	output, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(output), "prefix\n") || !strings.Contains(string(output), "retained") {
		t.Fatalf("managed log did not append complete output: %q", output)
	}
}

func TestJSONLinesRemainCompleteDuringConcurrentWrites(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	logger := slog.New(newJSONLineHandler(file))

	const count = 100
	var group sync.WaitGroup
	group.Add(count)
	for index := 0; index < count; index++ {
		go func(index int) {
			defer group.Done()
			emit(logger, slog.LevelDebug, "test.concurrent", "TEST", "worker complete", map[string]any{"index": index})
		}(index)
	}
	group.Wait()
	if syncErr := file.Sync(); syncErr != nil {
		t.Fatal(syncErr)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(file)
	seen := 0
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("line %d is not complete JSON: %v: %s", seen, err, scanner.Text())
		}
		if event["event"] != "test.concurrent" || event["message"] != "[TEST] worker complete" {
			t.Fatalf("line %d lost semantic fields: %#v", seen, event)
		}
		seen++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != count {
		t.Fatalf("got %d complete events, want %d", seen, count)
	}
}

func TestTraceLevelIsStableAndReadable(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "trace")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	logger := slog.New(newJSONLineHandler(file))
	emit(logger, slog.LevelDebug-4, "status.heartbeat", "SESSION", "still connected", nil)
	if syncErr := file.Sync(); syncErr != nil {
		t.Fatal(syncErr)
	}
	output, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(output, &event); err != nil {
		t.Fatal(err)
	}
	if event["level"] != "TRACE" || event["event"] != "status.heartbeat" || event["message"] != "[SESSION] still connected" {
		t.Fatalf("trace event lost stable vocabulary: %#v", event)
	}
}

func TestActiveLogIsNotTruncated(t *testing.T) {
	initMu.Lock()
	previousLogger := lg
	lg = &Logger{}
	initMu.Unlock()
	defer func() {
		initMu.Lock()
		if lg.file != nil {
			_ = lg.file.Close()
		}
		lg = previousLogger
		initMu.Unlock()
	}()

	path := filepath.Join(t.TempDir(), "private", "complete.jsonl")
	if err := SetPath(path); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("diagnostic-data ", 1024)
	for index := 0; index < 500; index++ {
		Info("FULL_LOG", payload, map[string]any{"index": index})
	}
	Info("FULL_LOG", "final complete retention marker", nil)

	initMu.Lock()
	syncErr := lg.file.Sync()
	initMu.Unlock()
	if syncErr != nil {
		t.Fatal(syncErr)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 4<<20 {
		t.Fatalf("active log size = %d, want more than the former 4 MiB retention threshold", info.Size())
	}
	output, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "final complete retention marker") {
		t.Fatal("final event was lost from the complete active log")
	}
}
