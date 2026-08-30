// Command dobby-cli is the native desktop operator client. It uses the
// authenticated SessionV2 control channel and never starts a JVM or a second
// VPN runtime.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go_module/grpcproto"
)

const (
	exitOK       = 0
	exitArgs     = 2
	exitConnect  = 3
	exitRuntime  = 4
	exitConflict = 8
	maxSource    = 1 << 20
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if isHelpCommand(args) {
		printHelp()
		return exitOK
	}
	// Log clearing is a local file operation. Keep it independent from the
	// control service so the reset remains usable before a service starts or
	// after one has failed, as required by the desktop qualification runners.
	if args[0] == "logs" {
		if len(args) != 2 || args[1] != "clear" {
			return usage("logs accepts only clear")
		}
		return clearApplicationLog()
	}
	conn, err := dialService()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dobby-cli: service unavailable")
		return exitConnect
	}
	defer func() { _ = conn.Close() }()
	client := grpcproto.NewVpnClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runServiceCommand(ctx, client, args)
}

func isHelpCommand(args []string) bool {
	return len(args) == 0 || args[0] == "--help" || args[0] == "-h"
}

func runServiceCommand(ctx context.Context, client grpcproto.VpnClient, args []string) int {
	switch args[0] {
	case "connect":
		if len(args) != 2 {
			return usage("connect requires a config path, URL, or inline TOML")
		}
		return connect(ctx, client, args[1], nil)
	case "connect-profile":
		if len(args) != 3 {
			return usage("connect-profile requires a source and profile index")
		}
		index, parseErr := parseProfileIndex(args[2])
		if parseErr != nil {
			return usage("profile index must be a non-negative integer")
		}
		return connect(ctx, client, args[1], &index)
	case "check-config":
		if len(args) != 2 {
			return usage("check-config requires a config path, URL, or inline TOML")
		}
		return checkConfig(ctx, client, args[1])
	case "disconnect":
		if len(args) != 1 {
			return usage("disconnect takes no arguments")
		}
		return disconnect(ctx, client)
	case "status":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			return usage("status accepts only --json")
		}
		return status(ctx, client, len(args) == 2)
	case "external-ip":
		return externalIP()
	case "verify-session":
		return verifySession(ctx, client)
	default:
		return usage("unknown command")
	}
}

func parseProfileIndex(value string) (int, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index < 0 || index > math.MaxInt32 {
		return 0, fmt.Errorf("profile index must fit in int32")
	}
	return index, nil
}

func initWindowsServiceLogger(
	ctx context.Context,
	client grpcproto.VpnClient,
	homeDirectory func() (string, error),
) error {
	home, err := homeDirectory()
	if err != nil {
		return err
	}
	if strings.TrimSpace(home) == "" {
		return fmt.Errorf("user home directory is empty")
	}
	path := windowsServiceLogPath(home)
	_, err = client.InitLogger(ctx, &grpcproto.InitLoggerRequest{
		Path: path,
	})
	return err
}

func windowsServiceLogPath(home string) string {
	return filepath.Join(home, ".dobbyvpn", "go_desktop_service_logs.jsonl")
}

func applicationLogPath(home string) string {
	// The current log is deliberately independent from the retired .myapp tree;
	// clearing or creating it never reads, moves, or deletes legacy diagnostics.
	return filepath.Join(home, ".dobbyvpn", "app_logs.txt")
}

func clearApplicationLog() int {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		fmt.Fprintln(os.Stderr, "dobby-cli: local application log unavailable")
		return exitRuntime
	}
	path := applicationLogPath(home)
	if err := clearLocalLogFileAtBase(path, home); err != nil {
		fmt.Fprintf(os.Stderr, "dobby-cli: local application log clear failed: %v\n", err)
		return exitRuntime
	}
	fmt.Println("LOGS_CLEARED")
	return exitOK
}

func initOptInServiceLogger(ctx context.Context, client grpcproto.VpnClient) error {
	path := strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH"))
	if path == "" {
		return nil
	}
	_, err := client.InitLogger(ctx, &grpcproto.InitLoggerRequest{Path: path})
	return err
}

func connect(ctx context.Context, client grpcproto.VpnClient, source string, profileIndex *int) int {
	raw, err := readSource(source)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dobby-cli: configuration source rejected")
		return exitArgs
	}
	if loggerErr := initServiceLogger(ctx, client); loggerErr != nil {
		fmt.Fprintln(os.Stderr, "dobby-cli: local service logging unavailable")
		return exitRuntime
	}
	profileValue, profileErr := profileIndexAsInt32(profileIndex)
	if profileErr != nil {
		fmt.Fprintln(os.Stderr, "dobby-cli: profile index rejected")
		return exitArgs
	}
	created, createErr := client.CreateSession(ctx, &grpcproto.SessionCreateSessionRequest{})
	if createErr != nil || created == nil || created.GetFailure() != nil {
		return reportFailure(createErr, failureOf(created))
	}
	sessionID := created.GetSessionId()
	keepSession := false
	var startedGeneration uint64
	defer func() {
		if !keepSession {
			cleanupSession(client, sessionID, startedGeneration)
		}
	}()
	configured, configureErr := client.Configure(ctx, &grpcproto.SessionConfigureRequest{
		SessionId: sessionID, CommandId: commandID("configure"), RawConfig: raw,
	})
	if configureErr != nil || configured == nil || configured.GetFailure() != nil {
		return reportFailure(configureErr, failureOf(configured))
	}
	started, startErr := startSession(ctx, client, sessionID, profileValue)
	if startErr != nil || started == nil || started.GetFailure() != nil {
		return reportFailure(startErr, failureOf(started))
	}
	startedGeneration = started.GetGeneration()
	result, connected := waitForConnection(ctx, client, sessionID)
	keepSession = connected
	return result
}

func initServiceLogger(ctx context.Context, client grpcproto.VpnClient) error {
	if runtime.GOOS == "windows" {
		return initWindowsServiceLogger(ctx, client, os.UserHomeDir)
	}
	return initOptInServiceLogger(ctx, client)
}

func profileIndexAsInt32(index *int) (*int32, error) {
	if index == nil {
		return nil, nil
	}
	if *index < 0 || *index > math.MaxInt32 {
		return nil, fmt.Errorf("profile index must fit in int32")
	}
	value := int32(*index)
	return &value, nil
}

func startSession(
	ctx context.Context,
	client grpcproto.VpnClient,
	sessionID string,
	profileIndex *int32,
) (*grpcproto.SessionStartResponse, error) {
	start := &grpcproto.SessionStartRequest{SessionId: sessionID, CommandId: commandID("start")}
	if profileIndex == nil {
		start.Mode = grpcproto.SessionStartMode_SESSION_START_MODE_AUTO_SELECT
	} else {
		start.Mode = grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX
		start.ProfileIndex = *profileIndex
	}
	return client.Start(ctx, start)
}

func waitForConnection(ctx context.Context, client grpcproto.VpnClient, sessionID string) (int, bool) {
	stream, err := client.Watch(ctx, &grpcproto.SessionObserveRequest{SessionId: sessionID})
	if err != nil {
		return reportFailure(err, nil), false
	}
	for {
		event, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return exitRuntime, false
		}
		if recvErr != nil {
			return reportFailure(recvErr, nil), false
		}
		switch event.GetState() {
		case grpcproto.SessionState_SESSION_STATE_UNSPECIFIED,
			grpcproto.SessionState_SESSION_STATE_IDLE,
			grpcproto.SessionState_SESSION_STATE_CONFIGURED,
			grpcproto.SessionState_SESSION_STATE_PROBING,
			grpcproto.SessionState_SESSION_STATE_PREPARING,
			grpcproto.SessionState_SESSION_STATE_STOPPING,
			grpcproto.SessionState_SESSION_STATE_DESTROYED:
			continue
		case grpcproto.SessionState_SESSION_STATE_CONNECTED:
			fmt.Println("CONNECTED")
			return exitOK, true
		case grpcproto.SessionState_SESSION_STATE_FAILED:
			fmt.Fprintln(os.Stderr, "dobby-cli: tunnel did not connect")
			return exitRuntime, false
		}
	}
}

func checkConfig(ctx context.Context, client grpcproto.VpnClient, source string) int {
	raw, err := readSource(source)
	if err != nil {
		return exitArgs
	}
	created, err := client.CreateSession(ctx, &grpcproto.SessionCreateSessionRequest{})
	if err != nil || created == nil || created.GetFailure() != nil {
		return reportFailure(err, failureOf(created))
	}
	id := created.GetSessionId()
	// Configuration is a read-only session operation, but the temporary
	// session still has to be destroyed when the command returns or its
	// request deadline expires.  Never let that deferred cleanup inherit an
	// unbounded context: an unresponsive service would otherwise keep
	// ``dobby-cli check-config`` alive after the 30-second command deadline and
	// consume the hosted lane's hard deadline.
	defer cleanupSession(client, id, 0)
	result, err := client.Configure(ctx, &grpcproto.SessionConfigureRequest{SessionId: id, CommandId: commandID("check"), RawConfig: raw})
	if err != nil || result == nil || result.GetFailure() != nil {
		return reportFailure(err, failureOf(result))
	}
	fmt.Printf("profiles=%d source=%s\n", len(result.GetProfiles()), result.GetSourceKind().String())
	return exitOK
}

func disconnect(ctx context.Context, client grpcproto.VpnClient) int {
	recovered, err := client.RecoverActiveSession(ctx, &grpcproto.Empty{})
	if err != nil {
		return reportFailure(err, nil)
	}
	if recovered == nil {
		return reportFailure(nil, nil)
	}
	if recovered.GetFailure() != nil {
		return reportRecoveredFailure(recovered.GetFailure())
	}
	id := recovered.GetSessionId()
	snapshot, err := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: id})
	if err != nil || snapshot == nil || snapshot.GetFailure() != nil {
		return reportFailure(err, failureOf(snapshot))
	}
	timedOut, stopFailure, stopErr := stopAndWaitForDisconnect(ctx, client, id, snapshot)
	if timedOut {
		return exitRuntime
	}
	if stopErr != nil || stopFailure != nil {
		return reportFailure(stopErr, stopFailure)
	}
	destroyFailure, destroyErr := destroySession(ctx, client, id)
	if destroyErr != nil || destroyFailure != nil {
		return reportFailure(destroyErr, destroyFailure)
	}
	fmt.Println("DISCONNECTED")
	return exitOK
}

func reportRecoveredFailure(failure *grpcproto.SessionFailure) int {
	if failure.GetCode() == grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND {
		fmt.Println("DISCONNECTED")
		return exitOK
	}
	return reportFailure(nil, failure)
}

func stopAndWaitForDisconnect(
	ctx context.Context,
	client grpcproto.VpnClient,
	sessionID string,
	snapshot *grpcproto.SessionSnapshotResponse,
) (bool, *grpcproto.SessionFailure, error) {
	current := snapshot.GetSnapshot()
	if current == nil {
		return false, nil, fmt.Errorf("service returned a snapshot response without a snapshot")
	}
	if current.GetGeneration() == 0 || current.GetCleanupComplete() {
		return false, nil, nil
	}
	generation := current.GetGeneration()
	stopped, stopErr := client.Stop(ctx, &grpcproto.SessionStopRequest{
		SessionId: sessionID, CommandId: commandID("stop"), Generation: generation,
	})
	if stopErr != nil {
		return false, nil, stopErr
	}
	if stopped == nil {
		return false, nil, fmt.Errorf("service returned an empty stop response")
	}
	if stopped.GetFailure() != nil {
		return false, failureOf(stopped), stopErr
	}
	for {
		current, getErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: sessionID})
		if getErr != nil {
			return false, nil, getErr
		}
		if current == nil {
			return false, nil, fmt.Errorf("service returned an empty snapshot response")
		}
		if current.GetFailure() != nil {
			return false, failureOf(current), getErr
		}
		currentSnapshot := current.GetSnapshot()
		if currentSnapshot == nil {
			return false, nil, fmt.Errorf("service returned a snapshot response without a snapshot")
		}
		if currentSnapshot.GetCleanupComplete() {
			return false, nil, nil
		}
		select {
		case <-ctx.Done():
			return true, nil, nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func destroySession(
	ctx context.Context,
	client grpcproto.VpnClient,
	sessionID string,
) (*grpcproto.SessionFailure, error) {
	destroyed, err := client.DestroySession(
		ctx,
		&grpcproto.SessionDestroySessionRequest{SessionId: sessionID},
	)
	if err != nil {
		return nil, err
	}
	if destroyed == nil {
		return nil, fmt.Errorf("service returned an empty destroy response")
	}
	if destroyed.GetFailure() != nil {
		return failureOf(destroyed), nil
	}
	if !destroyed.GetDestroyed() {
		return nil, fmt.Errorf("service did not confirm session destruction")
	}
	return nil, nil
}

func status(ctx context.Context, client grpcproto.VpnClient, jsonOutput bool) int {
	recovered, err := client.RecoverActiveSession(ctx, &grpcproto.Empty{})
	if err != nil {
		return reportFailure(err, nil)
	}
	state := grpcproto.SessionState_SESSION_STATE_IDLE
	var generation uint64
	if recovered == nil {
		return reportFailure(nil, nil)
	}
	if recovered.GetFailure() != nil {
		if recovered.GetFailure().GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND {
			return reportFailure(nil, failureOf(recovered))
		}
	} else if recovered.GetSessionId() != "" {
		snapshot, snapshotErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: recovered.GetSessionId()})
		if snapshotErr != nil || snapshot == nil || snapshot.GetFailure() != nil {
			return reportFailure(snapshotErr, failureOf(snapshot))
		}
		state, generation = snapshot.GetSnapshot().GetState(), snapshot.GetSnapshot().GetGeneration()
	}
	if jsonOutput {
		code, label := publicStatus(state)
		encoded, _ := json.Marshal(struct {
			Code  int    `json:"code"`
			State string `json:"state"`
		}{Code: code, State: label})
		fmt.Println(string(encoded))
	} else {
		fmt.Printf("state=%s generation=%d\n", state.String(), generation)
	}
	return exitOK
}

// publicStatus is the stable, machine-readable CLI contract consumed by the
// desktop qualification adapters. Keep the wire vocabulary independent from
// protobuf enum names so separate CLI processes can recover the same simple
// lifecycle contract across operating systems.
func publicStatus(state grpcproto.SessionState) (code int, label string) {
	code, label = 0, "Disconnected"
	switch state {
	case grpcproto.SessionState_SESSION_STATE_PROBING,
		grpcproto.SessionState_SESSION_STATE_PREPARING,
		grpcproto.SessionState_SESSION_STATE_CONFIGURED,
		grpcproto.SessionState_SESSION_STATE_STOPPING:
		code, label = 1, "Connecting"
	case grpcproto.SessionState_SESSION_STATE_CONNECTED:
		code, label = 2, "Connected"
	case grpcproto.SessionState_SESSION_STATE_UNSPECIFIED,
		grpcproto.SessionState_SESSION_STATE_IDLE,
		grpcproto.SessionState_SESSION_STATE_FAILED,
		grpcproto.SessionState_SESSION_STATE_DESTROYED:
	}
	return code, label
}

func verifySession(ctx context.Context, client grpcproto.VpnClient) int {
	return status(ctx, client, false)
}

func readSource(source string) ([]byte, error) {
	if len([]byte(source)) > maxSource {
		return nil, fmt.Errorf("source too large")
	}
	// A Windows drive path such as C:\\path\\config.toml parses as a URL
	// with scheme "c". Recognize it as a filesystem path before URL parsing;
	// otherwise the Windows desktop harness cannot pass a config file path.
	if sourceURL, isURL, err := parseSourceURL(source); isURL {
		if err != nil {
			return nil, err
		}
		return sourceURL, nil
	}
	return readSourceFileOrInline(source)
}

func parseSourceURL(source string) (urlSource []byte, isURL bool, err error) {
	if isWindowsPath(source) {
		return nil, false, nil
	}
	parsed, parseErr := url.Parse(source)
	if parseErr != nil {
		return nil, false, parseErr
	}
	if parsed.Scheme == "" {
		return nil, false, nil
	}
	if (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Host == "" {
		return nil, true, fmt.Errorf("source URL must use HTTP or HTTPS")
	}
	return []byte(source), true, nil
}

func readSourceFileOrInline(source string) ([]byte, error) {
	cleanPath := filepath.Clean(source)
	if data, err := os.ReadFile(cleanPath); err == nil {
		if len(data) > maxSource {
			return nil, fmt.Errorf("source too large")
		}
		return data, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read source")
	}
	return []byte(source), nil
}

func isWindowsPath(source string) bool {
	if len(source) < 3 || source[1] != ':' {
		return false
	}
	letter := source[0]
	return (letter >= 'A' && letter <= 'Z' || letter >= 'a' && letter <= 'z') &&
		(source[2] == '\\' || source[2] == '/')
}

// cleanupSession is best-effort but bounded. A CLI command which fails after
// creating a session must not leave a hidden tunnel or an undisposable
// session behind. The service remains authoritative for cleanup errors; the
// CLI only avoids printing raw diagnostic material here.
func cleanupSession(client grpcproto.VpnClient, sessionID string, generation uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, snapshotErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: sessionID})
	if snapshotErr == nil && snapshot != nil && snapshot.GetFailure() == nil {
		cleanupActiveSession(ctx, client, sessionID, snapshot, generation)
	}
	_, _ = client.DestroySession(ctx, &grpcproto.SessionDestroySessionRequest{SessionId: sessionID})
}

func cleanupActiveSession(
	ctx context.Context,
	client grpcproto.VpnClient,
	sessionID string,
	snapshot *grpcproto.SessionSnapshotResponse,
	generation uint64,
) {
	current := snapshot.GetSnapshot()
	if current == nil {
		return
	}
	if generation == 0 {
		generation = current.GetGeneration()
	}
	if current.GetCleanupComplete() || generation == 0 {
		return
	}
	_, _ = client.Stop(ctx, &grpcproto.SessionStopRequest{
		SessionId: sessionID, CommandId: commandID("cleanup-stop"), Generation: generation,
	})
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		currentSnapshot, snapshotErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: sessionID})
		if snapshotErr != nil || currentSnapshot == nil || currentSnapshot.GetFailure() != nil || currentSnapshot.GetSnapshot() == nil {
			return
		}
		if currentSnapshot.GetSnapshot().GetCleanupComplete() {
			return
		}
	}
}

func externalIP() int {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, endpoint := range []string{"https://api.ipify.org", "https://ifconfig.me/ip"} {
		request, requestErr := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, http.NoBody)
		if requestErr != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 256))
		closeErr := response.Body.Close()
		if closeErr != nil {
			continue
		}
		if readErr == nil && response.StatusCode >= 200 && response.StatusCode < 300 && strings.TrimSpace(string(body)) != "" {
			fmt.Println(strings.TrimSpace(string(body)))
			return exitOK
		}
	}
	return exitRuntime
}

func commandID(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

type failureResponse interface {
	GetFailure() *grpcproto.SessionFailure
}

func failureOf(response failureResponse) *grpcproto.SessionFailure {
	if response == nil {
		return nil
	}
	return response.GetFailure()
}

func reportFailure(err error, failure *grpcproto.SessionFailure) int {
	if failure != nil && failure.GetCode() == grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT {
		fmt.Fprintln(os.Stderr, "dobby-cli: session conflict")
		return exitConflict
	}
	if err != nil || failure == nil {
		fmt.Fprintln(os.Stderr, "dobby-cli: operation failed")
	} else {
		fmt.Fprintf(os.Stderr, "dobby-cli: operation failed (%s)\n", failure.GetCode().String())
	}
	return exitRuntime
}

func usage(message string) int {
	fmt.Fprintln(os.Stderr, "dobby-cli:", message)
	printHelp()
	return exitArgs
}

func printHelp() {
	fmt.Println("dobby-cli connect <source> | connect-profile <source> <index> | check-config <source> | disconnect | status [--json] | logs clear | external-ip | verify-session")
}
