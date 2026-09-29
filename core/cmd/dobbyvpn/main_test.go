package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"core/desktop/controljson"
	"core/sessionapi"
)

func TestReadSourceAcceptsHTTPSURLAndExistingFile(t *testing.T) {
	sourceURL := "https://example.invalid/subscription?token=secret"
	got, err := readSource(sourceURL)
	if err != nil || string(got) != sourceURL {
		t.Fatalf("readSource(%q) = %q, %v", sourceURL, got, err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("file config"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readSource(path)
	if err != nil || string(got) != "file config" {
		t.Fatalf("readSource(file) = %q, %v", got, err)
	}
}

func TestReadSourceRejectsMissingFileInsteadOfTreatingItAsInlineTOML(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.toml")
	for _, source := range []string{missingPath, "[[Xray]]\nName = \"inline\"\n"} {
		if _, err := readSource(source); err == nil || !strings.Contains(err.Error(), "cannot read configuration file") {
			t.Fatalf("readSource(%q) error = %v", source, err)
		}
	}
}

func TestInvalidConfigurationSourceUsesArgumentExitCode(t *testing.T) {
	err := &controljson.CallError{Code: string(sessionapi.FailureInvalidArgument), Message: "configuration file is missing"}
	if got := reportFailure(err); got != exitArgs {
		t.Fatalf("invalid source exit=%d, want %d", got, exitArgs)
	}
}

func TestReadSourceRejectsMalformedHTTPSURL(t *testing.T) {
	if _, err := readSource("https://[::1"); err == nil || !strings.Contains(err.Error(), "invalid configuration URL") {
		t.Fatalf("readSource(malformed HTTPS URL) error = %v", err)
	}
}

func TestParseSourceURLRequiresHTTPS(t *testing.T) {
	for _, source := range []string{"http://example.invalid/config", "ftp://example.invalid/config"} {
		if _, isURL, err := parseSourceURL(source); !isURL || err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
			t.Fatalf("parseSourceURL(%q) = isURL=%v, err=%v", source, isURL, err)
		}
	}
	if _, isURL, err := parseSourceURL("https://example.invalid/config"); !isURL || err != nil {
		t.Fatalf("valid HTTPS URL = isURL=%v, err=%v", isURL, err)
	}
	if _, _, err := parseSourceURL("https://[::1"); err == nil {
		t.Fatal("malformed HTTPS URL was accepted")
	}
}

func TestIsWindowsPathPreventsDriveLetterURLClassification(t *testing.T) {
	for _, source := range []string{`C:\Users\dobbytest\config.toml`, `z:/vpn/config.toml`} {
		if !isWindowsPath(source) {
			t.Fatalf("isWindowsPath(%q) = false", source)
		}
	}
	for _, source := range []string{"C:relative.toml", "/tmp/config.toml", "https://example.invalid/config"} {
		if isWindowsPath(source) {
			t.Fatalf("isWindowsPath(%q) = true", source)
		}
	}
}

func TestParseProfileIndexRejectsInvalidValues(t *testing.T) {
	if got, err := parseProfileIndex("2147483647"); err != nil || got != 2147483647 {
		t.Fatalf("parseProfileIndex(max int32) = %d, %v", got, err)
	}
	for _, value := range []string{"-1", "2147483648", "not-an-index"} {
		if _, err := parseProfileIndex(value); err == nil {
			t.Fatalf("parseProfileIndex(%q) unexpectedly succeeded", value)
		}
	}
}

func testControlClient(t *testing.T, respond func(controljson.Request) any) controljson.Client {
	t.Helper()
	return controljson.Client{Dial: func(context.Context) (net.Conn, error) {
		clientConn, serverConn := net.Pipe()
		go func() {
			defer serverConn.Close()
			var request controljson.Request
			if err := json.NewDecoder(serverConn).Decode(&request); err != nil {
				return
			}
			response, _ := json.Marshal(map[string]any{"ok": true, "result": respond(request)})
			_, _ = fmt.Fprintf(serverConn, "%s\n", response)
		}()
		return clientConn, nil
	}}
}

func TestConfigureAcceptsOneSourceAndReturnsBackendInventory(t *testing.T) {
	var methods []string
	var source string
	client := testControlClient(t, func(request controljson.Request) any {
		methods = append(methods, request.Method)
		if request.Method == "Snapshot" {
			return map[string]any{"session_id": "accepted-session", "sequence": 4}
		}
		var params struct {
			Source string `json:"source"`
		}
		_ = json.Unmarshal(request.Params, &params)
		source = params.Source
		return map[string]any{"digest": "digest-1", "sequence": 5,
			"profiles": []map[string]any{{"index": 0, "protocol": "Xray"}}}
	})
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[Xray]]\nName = \"test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := configureJSON(context.Background(), client, path); code != exitOK {
		t.Fatalf("configureJSON exit=%d", code)
	}
	if !reflect.DeepEqual(methods, []string{"Snapshot", "Configure"}) || source != "[[Xray]]\nName = \"test\"\n" {
		t.Fatalf("methods=%v source=%q", methods, source)
	}
}

func TestStartUsesAcceptedSnapshotWithoutSourceAndRejectsStaleDigest(t *testing.T) {
	var methods []string
	client := testControlClient(t, func(request controljson.Request) any {
		methods = append(methods, request.Method)
		switch request.Method {
		case "Snapshot":
			state := "CONFIGURED"
			if len(methods) == 3 {
				state = "CONNECTED"
			}
			return map[string]any{"session_id": "session", "digest": "digest", "sequence": 7,
				"configured": true, "state": state, "generation": 3}
		case "Start":
			var params struct {
				Mode  string `json:"mode"`
				Index int32  `json:"index"`
			}
			_ = json.Unmarshal(request.Params, &params)
			if params.Mode != string(sessionapi.ProfileIndex) || params.Index != 1 {
				t.Errorf("Start target = %+v", params)
			}
			return map[string]any{"sequence": 8, "generation": 3}
		}
		return map[string]any{}
	})
	if code := startJSON(context.Background(), client, []string{"--profile", "1", "--session-id", "session", "--config-digest", "digest"}); code != exitOK {
		t.Fatalf("startJSON exit=%d", code)
	}
	if !reflect.DeepEqual(methods, []string{"Snapshot", "Start", "Snapshot"}) {
		t.Fatalf("start methods = %v", methods)
	}
	methods = nil
	if code := startJSON(context.Background(), client, []string{"--profile", "1", "--session-id", "session", "--config-digest", "stale"}); code != exitConflict {
		t.Fatalf("stale start exit=%d", code)
	}
	if !reflect.DeepEqual(methods, []string{"Snapshot"}) {
		t.Fatalf("stale start methods = %v", methods)
	}
}
