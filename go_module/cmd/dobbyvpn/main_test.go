package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go_module/grpcproto"
	applicationlog "go_module/log"
	sessionv2 "go_module/sessionapi/v2"

	"google.golang.org/grpc"
)

func captureStderr(t *testing.T, operation func()) string {
	t.Helper()
	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = original })
	operation()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = original
	return string(output)
}

type disconnectClientStub struct {
	grpcproto.VpnClient
	stopResponse      *grpcproto.SessionStopResponse
	snapshotResponse  *grpcproto.SessionSnapshotResponse
	snapshotResponses []*grpcproto.SessionSnapshotResponse
	snapshotCall      int
	stopCalls         int
}

type loggerClientStub struct {
	grpcproto.VpnClient
	path string
}

func (stub *loggerClientStub) InitLogger(
	_ context.Context,
	request *grpcproto.InitLoggerRequest,
	_ ...grpc.CallOption,
) (*grpcproto.Empty, error) {
	stub.path = request.GetPath()
	return &grpcproto.Empty{}, nil
}

func (stub *disconnectClientStub) Stop(
	_ context.Context,
	request *grpcproto.SessionStopRequest,
	_ ...grpc.CallOption,
) (*grpcproto.SessionStopResponse, error) {
	stub.stopCalls++
	if stub.stopResponse != nil && stub.stopResponse.GetGeneration() != 0 &&
		stub.stopResponse.GetGeneration() != request.GetGeneration() {
		return nil, errors.New("stop generation mismatch")
	}
	return stub.stopResponse, nil
}

func (stub *disconnectClientStub) Snapshot(
	_ context.Context,
	_ *grpcproto.SessionSnapshotRequest,
	_ ...grpc.CallOption,
) (*grpcproto.SessionSnapshotResponse, error) {
	if len(stub.snapshotResponses) > 0 {
		index := stub.snapshotCall
		if index >= len(stub.snapshotResponses) {
			index = len(stub.snapshotResponses) - 1
		}
		stub.snapshotCall++
		return stub.snapshotResponses[index], nil
	}
	return stub.snapshotResponse, nil
}

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
	if writeErr := os.WriteFile(path, file, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	got, err = readSource(path)
	if err != nil || string(got) != string(file) {
		t.Fatalf("file source = %q, %v", got, err)
	}
}

func TestParseSourceURLRequiresHTTPS(t *testing.T) {
	for _, source := range []string{
		"http://example.invalid/config",
		"ftp://example.invalid/config",
	} {
		if _, isURL, err := parseSourceURL(source); !isURL || err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
			t.Fatalf("parseSourceURL(%q) = isURL=%v, err=%v; want HTTPS rejection", source, isURL, err)
		}
	}
	if _, isURL, err := parseSourceURL("https://example.invalid/config"); !isURL || err != nil {
		t.Fatalf("valid HTTPS URL = isURL=%v, err=%v", isURL, err)
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

func TestExplicitServiceLogPathTakesPrecedenceOnEveryDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	t.Setenv("DOBBY_LOG_PATH", path)
	client := &loggerClientStub{}
	if err := initServiceLogger(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if client.path != path {
		t.Fatalf("InitLogger path = %q, want %q", client.path, path)
	}
}

func TestApplicationLoggerUsesTheQualificationPath(t *testing.T) {
	applicationlog.Close()
	t.Cleanup(func() { _ = applicationlog.Close() })
	path := filepath.Join(t.TempDir(), "app.log")
	t.Setenv("DOBBY_CLI_LOG_PATH", path)
	if err := initApplicationLogger(); err != nil {
		t.Fatal(err)
	}
	applicationlog.Info("CLI", "test application event", nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test application event") {
		t.Fatalf("application log = %q, want the CLI event", data)
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
	t.Setenv("USERPROFILE", home)
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

func TestReadSourceRejectsUnsupportedURL(t *testing.T) {
	for _, source := range []string{"ftp://example.invalid/config", "file:///tmp/config"} {
		if _, err := readSource(source); err == nil {
			t.Fatalf("readSource(%q) unexpectedly succeeded", source)
		}
	}
}

func TestParseProfileIndexRejectsValuesOutsideProtoRange(t *testing.T) {
	if got, err := parseProfileIndex("2147483647"); err != nil || got != 2147483647 {
		t.Fatalf("parseProfileIndex(max int32) = %d, %v", got, err)
	}
	for _, value := range []string{"-1", "2147483648", "not-an-index"} {
		if _, err := parseProfileIndex(value); err == nil {
			t.Fatalf("parseProfileIndex(%q) unexpectedly succeeded", value)
		}
	}
}

func TestProfileInventoryJSONPreservesDynamicOrderAndProtocol(t *testing.T) {
	profiles := []sessionv2.ProfileSummary{
		{Index: 0, Protocol: sessionv2.ProtocolXray},
		{Index: 1, Protocol: sessionv2.ProtocolOutline},
		{Index: 2, Protocol: sessionv2.ProtocolTrustTunnel},
	}
	got, err := profileInventoryJSON(profiles)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"profiles":[{"index":0,"protocol":"XRAY"},{"index":1,"protocol":"OUTLINE"},{"index":2,"protocol":"TRUST_TUNNEL"}]}`
	if string(got) != want {
		t.Fatalf("profile inventory = %s, want %s", got, want)
	}
}

func TestProfileInventoryJSONRejectsIncompleteIdentity(t *testing.T) {
	for _, profiles := range [][]sessionv2.ProfileSummary{
		{{Index: -1, Protocol: sessionv2.ProtocolOutline}},
		{{Index: 0, Protocol: sessionv2.Protocol("")}},
	} {
		if _, err := profileInventoryJSON(profiles); err == nil {
			t.Fatalf("invalid profiles unexpectedly accepted: %#v", profiles)
		}
	}
}

func TestStopAndWaitRejectsEmptyServiceResponses(t *testing.T) {
	initial := &grpcproto.SessionSnapshot{SessionId: "session", Generation: 1}
	tests := []struct {
		name   string
		client disconnectClientStub
	}{
		{name: "stop", client: disconnectClientStub{}},
		{
			name: "snapshot",
			client: disconnectClientStub{
				stopResponse: &grpcproto.SessionStopResponse{Generation: 1},
			},
		},
		{
			name: "snapshot payload",
			client: disconnectClientStub{
				stopResponse:     &grpcproto.SessionStopResponse{Generation: 1},
				snapshotResponse: &grpcproto.SessionSnapshotResponse{},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure, err := stopAndWaitForDisconnect(context.Background(), &test.client, initial)
			if err == nil {
				t.Fatal("empty service response unexpectedly succeeded")
			}
			if failure != nil {
				t.Fatalf("empty service response returned protocol failure: %v", failure)
			}
		})
	}
}

func TestDisconnectRejectsMalformedSnapshots(t *testing.T) {
	for _, snapshotResponse := range []*grpcproto.SessionSnapshotResponse{nil, {}} {
		client := &disconnectClientStub{snapshotResponse: snapshotResponse}
		if got := disconnect(context.Background(), client); got != exitRuntime {
			t.Fatalf("disconnect malformed snapshot exit=%d, want %d", got, exitRuntime)
		}
	}
}

func TestDisconnectRetainsConfiguredSession(t *testing.T) {
	client := &disconnectClientStub{snapshotResponse: &grpcproto.SessionSnapshotResponse{
		Snapshot: &grpcproto.SessionSnapshot{
			SessionId: "session", State: grpcproto.SessionState_SESSION_STATE_CONFIGURED,
			Configured: true, Digest: "accepted", CleanupComplete: true,
		},
	}}
	if got := disconnect(context.Background(), client); got != exitOK {
		t.Fatalf("disconnect exit=%d, want %d", got, exitOK)
	}
	if client.stopCalls != 0 {
		t.Fatalf("disconnect stopped an idle configured session %d times", client.stopCalls)
	}
}

func TestDisconnectReportsCleanupFailureFromCompletedSnapshot(t *testing.T) {
	cleanupFailure := &grpcproto.SessionFailure{
		Code:    grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED,
		Message: "tunnel release failed",
	}
	tests := []struct {
		name              string
		snapshotResponses []*grpcproto.SessionSnapshotResponse
		stopResponse      *grpcproto.SessionStopResponse
		wantStopCalls     int
	}{
		{
			name: "already complete",
			snapshotResponses: []*grpcproto.SessionSnapshotResponse{{
				Snapshot: &grpcproto.SessionSnapshot{
					SessionId: "session", Generation: 1, State: grpcproto.SessionState_SESSION_STATE_FAILED,
					CleanupComplete: true, LastFailure: cleanupFailure,
				},
			}},
		},
		{
			name:          "completes after stop",
			stopResponse:  &grpcproto.SessionStopResponse{Generation: 1},
			wantStopCalls: 1,
			snapshotResponses: []*grpcproto.SessionSnapshotResponse{
				{Snapshot: &grpcproto.SessionSnapshot{
					SessionId: "session", Generation: 1, State: grpcproto.SessionState_SESSION_STATE_CONNECTED,
				}},
				{Snapshot: &grpcproto.SessionSnapshot{
					SessionId: "session", Generation: 1, State: grpcproto.SessionState_SESSION_STATE_FAILED,
					CleanupComplete: true, LastFailure: cleanupFailure,
				}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &disconnectClientStub{
				stopResponse:      test.stopResponse,
				snapshotResponses: test.snapshotResponses,
			}
			var exitCode int
			output := captureStderr(t, func() { exitCode = disconnect(context.Background(), client) })
			if exitCode != exitRuntime {
				t.Fatalf("disconnect exit=%d, want %d", exitCode, exitRuntime)
			}
			if client.stopCalls != test.wantStopCalls {
				t.Fatalf("disconnect stop calls=%d, want %d", client.stopCalls, test.wantStopCalls)
			}
			for _, expected := range []string{"CLEANUP_FAILED", "tunnel release failed"} {
				if !strings.Contains(output, expected) {
					t.Fatalf("disconnect error %q lacks %q", output, expected)
				}
			}
		})
	}
}

func TestReportFailureUsesConflictExitCodeOnlyForConflict(t *testing.T) {
	output := captureStderr(t, func() {
		conflict := &grpcproto.SessionFailure{
			Code:    grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT,
			Message: "exact conflict detail",
		}
		if got := reportFailure(errors.New("exact transport detail"), conflict); got != exitConflict {
			t.Fatalf("conflict exit=%d, want %d", got, exitConflict)
		}
		unsupported := &grpcproto.SessionFailure{Code: grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_UNSUPPORTED}
		if got := reportFailure(nil, unsupported); got != exitRuntime {
			t.Fatalf("unsupported exit=%d, want %d", got, exitRuntime)
		}
	})
	for _, expected := range []string{
		"exact transport detail",
		"SESSION_FAILURE_CODE_CONFLICT",
		"exact conflict detail",
		"SESSION_FAILURE_CODE_UNSUPPORTED",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("failure output %q lacks %q", output, expected)
		}
	}
}

func TestCleanupSessionNeverStopsANewerGeneration(t *testing.T) {
	client := &disconnectClientStub{snapshotResponse: &grpcproto.SessionSnapshotResponse{
		Snapshot: &grpcproto.SessionSnapshot{
			SessionId: "session", Generation: 2, CleanupComplete: true,
			LastFailure: &grpcproto.SessionFailure{
				Code:    grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED,
				Message: "newer generation cleanup failed",
			},
		},
	}}
	if err := cleanupSession(client, "session", 1); err != nil {
		t.Fatal(err)
	}
	if client.stopCalls != 0 {
		t.Fatalf("cleanup stopped a newer generation %d times", client.stopCalls)
	}
}

func TestCleanupSessionReportsCompletedCleanupFailureForItsGeneration(t *testing.T) {
	client := &disconnectClientStub{snapshotResponse: &grpcproto.SessionSnapshotResponse{
		Snapshot: &grpcproto.SessionSnapshot{
			SessionId: "session", Generation: 1, CleanupComplete: true,
			LastFailure: &grpcproto.SessionFailure{
				Code:    grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED,
				Message: "tunnel release failed",
			},
		},
	}}
	err := cleanupSession(client, "session", 1)
	if err == nil {
		t.Fatal("cleanupSession accepted a completed cleanup failure")
	}
	for _, expected := range []string{"CLEANUP_FAILED", "tunnel release failed"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("cleanupSession error %q lacks %q", err, expected)
		}
	}
	if client.stopCalls != 0 {
		t.Fatalf("cleanup retried completed cleanup %d times", client.stopCalls)
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
