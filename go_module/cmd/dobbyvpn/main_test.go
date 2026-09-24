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

	"go_module/desktop_exports/controljson"
	"go_module/sessionapi"
)

func TestReadSourceAcceptsInlineURLAndFileWithoutReadingURL(t *testing.T) {
	for _, source := range []string{
		"https://example.invalid/subscription?token=secret",
		"[[Xray]]\nName = \"inline\"\n",
	} {
		got, err := readSource(source)
		if err != nil || string(got) != source {
			t.Fatalf("readSource(%q) = %q, %v", source, got, err)
		}
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

func TestParseSourceURLRequiresHTTPS(t *testing.T) {
	for _, source := range []string{"http://example.invalid/config", "ftp://example.invalid/config"} {
		if _, isURL, err := parseSourceURL(source); !isURL || err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
			t.Fatalf("parseSourceURL(%q) = isURL=%v, err=%v", source, isURL, err)
		}
	}
	if _, isURL, err := parseSourceURL("https://example.invalid/config"); !isURL || err != nil {
		t.Fatalf("valid HTTPS URL = isURL=%v, err=%v", isURL, err)
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

func TestPublicStatusUsesStableMachineReadableContract(t *testing.T) {
	cases := map[sessionapi.State]struct {
		code  int
		state string
	}{
		sessionapi.StateIdle:       {0, "Disconnected"},
		sessionapi.StateConfigured: {1, "Connecting"},
		sessionapi.StateProbing:    {1, "Connecting"},
		sessionapi.StatePreparing:  {1, "Connecting"},
		sessionapi.StateConnected:  {2, "Connected"},
		sessionapi.StateStopping:   {1, "Connecting"},
		sessionapi.StateFailed:     {0, "Disconnected"},
	}
	for state, want := range cases {
		gotCode, gotState := publicStatus(string(state))
		if gotCode != want.code || gotState != want.state {
			t.Errorf("publicStatus(%s) = (%d, %q), want (%d, %q)", state, gotCode, gotState, want.code, want.state)
		}
	}
}

func TestConnectUsesOnlyFourDesktopCommands(t *testing.T) {
	var methods []string
	var configuredSource string
	client := controljson.Client{Dial: func(context.Context) (net.Conn, error) {
		clientConn, serverConn := net.Pipe()
		go func() {
			defer serverConn.Close()
			var request controljson.Request
			if err := json.NewDecoder(serverConn).Decode(&request); err != nil {
				return
			}
			methods = append(methods, request.Method)
			var result any
			switch request.Method {
			case "Snapshot":
				var params struct {
					SessionID string `json:"session_id"`
				}
				_ = json.Unmarshal(request.Params, &params)
				state := "CONFIGURED"
				if len(methods) == 4 {
					state = "CONNECTED"
				}
				result = map[string]any{"session_id": "session", "sequence": 3, "generation": 9, "state": state, "cleanup_complete": false}
			case "Configure":
				var params struct {
					Source string `json:"source"`
				}
				_ = json.Unmarshal(request.Params, &params)
				configuredSource = params.Source
				result = map[string]any{"sequence": 2}
			case "Start":
				result = map[string]any{"sequence": 3, "generation": 9}
			default:
				result = map[string]any{}
			}
			response, _ := json.Marshal(map[string]any{"ok": true, "result": result})
			_, _ = fmt.Fprintf(serverConn, "%s\n", response)
		}()
		return clientConn, nil
	}}
	if got := connect(context.Background(), client, "[[Xray]]\nName = \"test\"\n", nil); got != exitOK {
		t.Fatalf("connect exit=%d", got)
	}
	if !reflect.DeepEqual(methods, []string{"Snapshot", "Configure", "Start", "Snapshot"}) {
		t.Fatalf("desktop commands = %v", methods)
	}
	if configuredSource != "[[Xray]]\nName = \"test\"\n" {
		t.Fatalf("configured source = %q", configuredSource)
	}
}
