package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go_module/grpcproto"
)

func TestReadSourceAcceptsInlineURLAndFileWithoutReadingURL(t *testing.T) {
	inline := "[[Outline]]\nServer='vpn.invalid'\n"
	got, err := readSource(inline)
	if err != nil || string(got) != inline {
		t.Fatalf("inline source = %q, %v", got, err)
	}
	urlSource := "HTTPS://example.invalid/config?token=synthetic"
	got, err = readSource(urlSource)
	if err != nil || string(got) != urlSource {
		t.Fatalf("URL source = %q, %v", got, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	file := []byte("[[Xray]]\noutbounds=[]\n")
	if err := os.WriteFile(path, file, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = readSource(path)
	if err != nil || string(got) != string(file) {
		t.Fatalf("file source = %q, %v", got, err)
	}
}

func TestIsWindowsPathPreventsDriveLetterURLClassification(t *testing.T) {
	for _, source := range []string{`C:\\Users\\dobbytest\\config.toml`, `z:/vpn/config.toml`} {
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

func TestWindowsServiceLogPathMatchesDesktopContract(t *testing.T) {
	home := filepath.FromSlash(`C:/Users/dobbytest`)
	want := filepath.FromSlash(`C:/Users/dobbytest/.dobbyvpn/go_desktop_service_logs.jsonl`)
	if got := windowsServiceLogPath(home); got != want {
		t.Fatalf("windowsServiceLogPath(%q) = %q, want %q", home, got, want)
	}
}

func TestFreshCurrentLogLeavesLegacyLogUntouched(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".myapp", "app_logs.txt")
	current := applicationLogPath(home)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy remains\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearLocalLogFile(current); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != localLogClearMarker {
		t.Fatalf("fresh current log = %q, want marker %q", data, localLogClearMarker)
	}
	legacyData, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacyData) != "legacy remains\n" {
		t.Fatalf("legacy log changed = %q", legacyData)
	}
}

func TestClearLocalLogFileRemovesHistoryAndWritesBoundaryMarker(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".dobbyvpn", "app_logs.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old diagnostic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := clearLocalLogFile(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != localLogClearMarker {
		t.Fatalf("cleared log = %q, want marker %q", data, localLogClearMarker)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cleared log permissions = %v, %v", info, err)
	}
}

func TestClearLocalLogFileRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.log")
	link := filepath.Join(root, "app_logs.txt")
	if err := os.WriteFile(target, []byte("must remain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := clearLocalLogFile(link); err == nil {
		t.Fatal("symlink log path unexpectedly cleared")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "must remain\n" {
		t.Fatalf("symlink target changed: %q", data)
	}
}

func TestClearLocalLogFileRejectsSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDir, "app_logs.txt")
	if err := os.WriteFile(target, []byte("must remain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, ".dobbyvpn")
	if err := os.Symlink(targetDir, aliasDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := clearLocalLogFile(filepath.Join(aliasDir, "app_logs.txt")); err == nil {
		t.Fatal("clear accepted a symlinked parent")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "must remain\n" {
		t.Fatalf("symlink target changed after parent rejection: %q", data)
	}
}

func TestLogsClearDoesNotRequireControlService(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	path := applicationLogPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old application diagnostic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := run([]string{"logs", "clear"}); got != exitOK {
		t.Fatalf("logs clear exit=%d, want %d", got, exitOK)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != localLogClearMarker {
		t.Fatalf("logs clear wrote %q, want marker %q", data, localLogClearMarker)
	}
}

func TestReadSourceRejectsUnsupportedURLAndOversize(t *testing.T) {
	for _, source := range []string{"ftp://example.invalid/config", "file:///tmp/config"} {
		if _, err := readSource(source); err == nil {
			t.Fatalf("readSource(%q) unexpectedly succeeded", source)
		}
	}
	if _, err := readSource(strings.Repeat("x", maxSource+1)); err == nil {
		t.Fatal("oversize source unexpectedly succeeded")
	}
}

func TestReportFailureUsesConflictExitCodeOnlyForConflict(t *testing.T) {
	conflict := &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT}
	if got := reportFailure(nil, conflict); got != exitConflict {
		t.Fatalf("conflict exit=%d, want %d", got, exitConflict)
	}
	unsupported := &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED}
	if got := reportFailure(nil, unsupported); got != exitRuntime {
		t.Fatalf("unsupported exit=%d, want %d", got, exitRuntime)
	}
}

func TestPublicStatusUsesStableMachineReadableContract(t *testing.T) {
	tests := []struct {
		name  string
		state grpcproto.SessionState
		code  int
		label string
	}{
		{name: "idle", state: grpcproto.SessionState_SESSION_STATE_IDLE, code: 0, label: "Disconnected"},
		{name: "configured", state: grpcproto.SessionState_SESSION_STATE_CONFIGURED, code: 1, label: "Connecting"},
		{name: "probing", state: grpcproto.SessionState_SESSION_STATE_PROBING, code: 1, label: "Connecting"},
		{name: "preparing", state: grpcproto.SessionState_SESSION_STATE_PREPARING, code: 1, label: "Connecting"},
		{name: "connected", state: grpcproto.SessionState_SESSION_STATE_CONNECTED, code: 2, label: "Connected"},
		{name: "stopping", state: grpcproto.SessionState_SESSION_STATE_STOPPING, code: 1, label: "Connecting"},
		{name: "failed", state: grpcproto.SessionState_SESSION_STATE_FAILED, code: 0, label: "Disconnected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, label := publicStatus(test.state)
			if code != test.code || label != test.label {
				t.Fatalf("publicStatus(%s) = (%d, %q), want (%d, %q)", test.state, code, label, test.code, test.label)
			}
		})
	}
}
