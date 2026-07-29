package log

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestLocalLogsRedactURLsCredentialsConfigurationAndMetadata(t *testing.T) {
	message := maskMessage("connect https://user:password@example.invalid/path token=super-secret")
	structured := fmt.Sprint(redactValue("metadata", map[string]any{
		"apiToken": "another-secret",
		"password": "nested-secret",
		"safe":     "value",
	}))
	message += structured
	for _, secret := range []string{"user:password", "super-secret", "another-secret", "nested-secret"} {
		if strings.Contains(message, secret) {
			t.Fatalf("log leaked %q: %s", secret, message)
		}
	}
	if !strings.Contains(message, "[REDACTED") {
		t.Fatalf("log was not redacted: %s", message)
	}
	for _, rawConfig := range []string{
		"{\n  \"token\": \"pretty-json-secret\"\n}",
		"config={\"UID\":\"generic-json-secret\"}",
		"[Outline]\nserver = \"vpn.example.invalid\"\npassword = \"toml-secret\"",
	} {
		if got := redactText(rawConfig); !strings.Contains(got, "[REDACTED") {
			t.Fatalf("configuration was not redacted: %s", got)
		}
	}
	fields := logrus.Fields{"metadata": map[string]any{"Authorization": "Bearer nested-secret"}}
	if got := fmt.Sprint(redactValue("metadata", fields)); strings.Contains(got, "nested-secret") {
		t.Fatalf("named structured fields leaked a secret: %s", got)
	}

	file, err := os.CreateTemp(t.TempDir(), "handler")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	handler := newJSONLineHandler(file)
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "Password=handler-secret", 0)
	record.AddAttrs(
		slog.String("endpoint", "vpn.example.invalid:443"),
		slog.Any("failure", fmt.Errorf("dial vpn.example.invalid: nested-secret")),
		slog.String("source", "https://source-secret@example.invalid/private"),
		slog.String("event", "https://event-secret@example.invalid/private"),
	)
	if handleErr := handler.Handle(context.Background(), record); handleErr != nil {
		t.Fatal(handleErr)
	}
	if syncErr := file.Sync(); syncErr != nil {
		t.Fatal(syncErr)
	}
	output, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "handler-secret") ||
		strings.Contains(string(output), "vpn.example.invalid") ||
		strings.Contains(string(output), "nested-secret") ||
		strings.Contains(string(output), "source-secret") ||
		strings.Contains(string(output), "event-secret") {
		t.Fatalf("handler leaked secret: %s", output)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("handler output is not one JSON object: %v: %s", err, output)
	}
	if decoded["message"] != "Password=[REDACTED]" || decoded["endpoint"] != "[REDACTED]" {
		t.Fatalf("handler did not redact fields: %#v", decoded)
	}
}

func TestLocalLogsRedactVPNNetworkLocationsButKeepOperationalFacts(t *testing.T) {
	message := maskMessage(
		"stage=protocol_ready remote=vpn.example.invalid:443 proxy=random-user:random-password@127.0.0.1:1080 gateway=198.51.100.5 library_value=203.0.113.8 ipv6=fd00:dbb::1 lookup=vpn.example.invalid source=logger.go elapsed=42ms",
	)
	for _, location := range []string{"vpn.example.invalid", "random-user", "random-password", "127.0.0.1", "198.51.100.5", "fd00:dbb::1", "vpn.example.invalid", ":443", ":1080"} {
		if strings.Contains(message, location) {
			t.Fatalf("log leaked network location fragment %q: %s", location, message)
		}
	}
	for _, fact := range []string{"stage=protocol_ready", "source=logger.go", "elapsed=42ms", "[REDACTED ENDPOINT]"} {
		if !strings.Contains(message, fact) {
			t.Fatalf("log lost useful operational fact %q: %s", fact, message)
		}
	}
}

func TestSetPathUsesOwnerOnlyFileAndDirectoryPermissions(t *testing.T) {
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
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("log file permissions = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("log directory permissions = %o, want 700", got)
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
	if strings.Contains(string(output), "startup-secret") || !strings.Contains(string(output), "startup status=idle") {
		t.Fatalf("buffered event lost meaning or leaked a secret: %s", output)
	}
	if timestamp, _ := event["timestamp"].(string); strings.HasPrefix(timestamp, "0001-") || timestamp == "" {
		t.Fatalf("buffered event has invalid fallback timestamp: %#v", event)
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
	if err := file.Sync(); err != nil {
		t.Fatal(err)
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
	if err := file.Sync(); err != nil {
		t.Fatal(err)
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

func TestRetentionKeepsNewestCompleteJSONLines(t *testing.T) {
	input := []byte("{\"id\":1}\n{\"id\":2}\n{\"id\":3}\nincomplete")
	retained := retainNewestCompleteJSONLLines(input, int64(len("{\"id\":2}\n{\"id\":3}\n")))
	if got, want := string(retained), "{\"id\":2}\n{\"id\":3}\n"; got != want {
		t.Fatalf("retained = %q, want %q", got, want)
	}
	if strings.Contains(string(retained), "incomplete") {
		t.Fatalf("retention kept an incomplete record: %q", retained)
	}
}

func TestActiveLogRetentionIsBoundedAndKeepsFinalEvent(t *testing.T) {
	initMu.Lock()
	previousLogger := lg
	previousLimit := maxLocalLogBytes
	lg = &Logger{}
	maxLocalLogBytes = 1024
	initMu.Unlock()
	defer func() {
		initMu.Lock()
		if lg.file != nil {
			_ = lg.file.Close()
		}
		lg = previousLogger
		maxLocalLogBytes = previousLimit
		initMu.Unlock()
	}()

	path := filepath.Join(t.TempDir(), "private", "bounded.jsonl")
	if err := SetPath(path); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 80; index++ {
		Info("RETENTION", strings.Repeat("status ", 12), map[string]any{"index": index})
	}
	Info("RETENTION", "final retention marker", nil)
	if err := lg.file.Sync(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxLocalLogBytes {
		t.Fatalf("bounded log size = %d, limit = %d", info.Size(), maxLocalLogBytes)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("retained log permissions = %o, want 600", info.Mode().Perm())
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	finalSeen := false
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("retained line is not complete JSON: %v: %s", err, scanner.Text())
		}
		if event["message"] == "[RETENTION] final retention marker" {
			finalSeen = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !finalSeen {
		t.Fatal("final event was lost during retention")
	}
}
