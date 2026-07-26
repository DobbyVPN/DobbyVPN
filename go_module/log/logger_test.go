package log

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestLocalLogsRedactURLsCredentialsConfigurationAndMetadata(t *testing.T) {
	message := prepareLog(
		"connect https://user:password@example.invalid/path token=super-secret",
		map[string]any{
			"apiToken": "another-secret",
			"metadata": map[string]any{"password": "nested-secret", "safe": "value"},
		},
	)
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
	handler := &simpleHandler{file: file}
	if handleErr := handler.Handle(context.Background(), slog.NewRecord(time.Time{}, slog.LevelInfo, "Password=handler-secret", 0)); handleErr != nil {
		t.Fatal(handleErr)
	}
	if syncErr := file.Sync(); syncErr != nil {
		t.Fatal(syncErr)
	}
	output, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "handler-secret") {
		t.Fatalf("handler leaked secret: %s", output)
	}
}

func TestLocalLogsRedactVPNNetworkLocationsButKeepOperationalFacts(t *testing.T) {
	message := prepareLog(
		"stage=protocol_ready remote=vpn.example.invalid:443 proxy=127.0.0.1:1080 gateway=198.51.100.5 library_value=203.0.113.8 source=logger.go elapsed=42ms",
		nil,
	)
	for _, location := range []string{"vpn.example.invalid", "127.0.0.1", "198.51.100.5", ":443", ":1080"} {
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
	lg = &Logger{debugBuf: []string{}, infoBuf: []string{}, warnBuf: []string{}, errorBuf: []string{}}
	initMu.Unlock()
	defer func() {
		initMu.Lock()
		if lg.file != nil {
			_ = lg.file.Close()
		}
		lg = previous
		initMu.Unlock()
	}()

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
}
