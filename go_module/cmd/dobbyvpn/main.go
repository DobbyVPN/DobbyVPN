// Command dobby-cli is the native desktop operator client. It uses the
// authenticated session control channel and never starts a JVM or a second
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
	applicationlog "go_module/log"
	"go_module/sessionapi"
)

const (
	exitOK       = 0
	exitArgs     = 2
	exitConnect  = 3
	exitRuntime  = 4
	exitConflict = 8
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
	if err := initApplicationLogger(); err != nil {
		fmt.Fprintf(os.Stderr, "dobby-cli: application logging unavailable errorType=%T error=%v\n", err, err)
		return exitRuntime
	}
	applicationlog.Info("CLI", "dobby-cli command started", map[string]any{"command": args[0]})
	if args[0] == "profile-inventory" {
		if len(args) != 2 {
			return usage("profile-inventory requires a config path or inline TOML")
		}
		return profileInventory(args[1])
	}
	conn, err := dialService()
	if err != nil {
		reportCLIError("service connection failed", err)
		return exitConnect
	}
	client := grpcproto.NewVpnClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := runServiceCommand(ctx, client, args)
	if closeErr := conn.Close(); closeErr != nil {
		reportCLIError("service connection cleanup failed", closeErr)
		if result == exitOK {
			return exitRuntime
		}
	}
	return result
}

func initApplicationLogger() error {
	path := strings.TrimSpace(os.Getenv("DOBBY_CLI_LOG_PATH"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if strings.TrimSpace(home) == "" {
			return errors.New("user home directory is empty")
		}
		path = applicationLogPath(home)
	}
	return applicationlog.SetPath(path)
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
			reportCLIError("profile index rejected", parseErr)
			return exitArgs
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

func parseProfileIndex(value string) (int32, error) {
	index, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse profile index: %w", err)
	}
	if index < 0 || index > math.MaxInt32 {
		return 0, fmt.Errorf("profile index must fit in int32")
	}
	return int32(index), nil
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
	if err != nil {
		reportCLIError("local application log home lookup failed", err)
		return exitRuntime
	}
	if strings.TrimSpace(home) == "" {
		reportCLIError("local application log unavailable", errors.New("user home directory is empty"))
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

func connect(ctx context.Context, client grpcproto.VpnClient, source string, profileIndex *int32) int {
	raw, err := readSource(source)
	if err != nil {
		reportCLIError("configuration source rejected", err)
		return exitArgs
	}
	if loggerErr := initServiceLogger(ctx, client); loggerErr != nil {
		reportCLIError("local service logging unavailable", loggerErr)
		return exitRuntime
	}
	current, snapshotErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{})
	if snapshotErr != nil || current == nil || current.GetFailure() != nil {
		return reportFailure(snapshotErr, failureOf(current))
	}
	owner := current.GetSnapshot()
	if owner == nil {
		return reportFailure(errors.New("service returned a snapshot response without a snapshot"), nil)
	}
	sessionID := owner.GetSessionId()
	keepSession := false
	var startedGeneration uint64
	defer func() {
		if !keepSession && startedGeneration != 0 {
			if cleanupErr := cleanupSession(client, sessionID, startedGeneration); cleanupErr != nil {
				reportCLIError("failed connection session cleanup failed", cleanupErr)
			}
		}
	}()
	configured, configureErr := client.Configure(ctx, &grpcproto.SessionConfigureRequest{
		SessionId: sessionID, ExpectedSequence: owner.GetSequence(), RawConfig: raw,
	})
	if configureErr != nil || configured == nil || configured.GetFailure() != nil {
		return reportFailure(configureErr, failureOf(configured))
	}
	started, startErr := startSession(ctx, client, sessionID, configured.GetSequence(), profileIndex)
	if startErr != nil || started == nil || started.GetFailure() != nil {
		return reportFailure(startErr, failureOf(started))
	}
	startedGeneration = started.GetGeneration()
	result, connected := waitForConnection(ctx, client, sessionID)
	keepSession = connected
	return result
}

func initServiceLogger(ctx context.Context, client grpcproto.VpnClient) error {
	// Qualification supplies a request-confined path on every desktop. Honor
	// that explicit interface before the ordinary Windows user-log default so
	// no service log byte escapes the retained request tree.
	if strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH")) != "" {
		return initOptInServiceLogger(ctx, client)
	}
	if runtime.GOOS == "windows" {
		return initWindowsServiceLogger(ctx, client, os.UserHomeDir)
	}
	return initOptInServiceLogger(ctx, client)
}

func startSession(
	ctx context.Context,
	client grpcproto.VpnClient,
	sessionID string,
	expectedSequence uint64,
	profileIndex *int32,
) (*grpcproto.SessionStartResponse, error) {
	start := &grpcproto.SessionStartRequest{SessionId: sessionID, ExpectedSequence: expectedSequence}
	if profileIndex == nil {
		start.Mode = grpcproto.SessionStartMode_SESSION_START_MODE_AUTO_SELECT
	} else {
		start.Mode = grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX
		start.ProfileIndex = *profileIndex
	}
	return client.Start(ctx, start)
}

func waitForConnection(ctx context.Context, client grpcproto.VpnClient, sessionID string) (int, bool) {
	stream, err := client.Watch(ctx, &grpcproto.SessionSnapshotRequest{SessionId: sessionID})
	if err != nil {
		return reportFailure(err, nil), false
	}
	for {
		snapshot, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			reportCLIError("session snapshot stream ended", io.EOF)
			return exitRuntime, false
		}
		if recvErr != nil {
			return reportFailure(recvErr, nil), false
		}
		switch snapshot.GetState() {
		case grpcproto.SessionState_SESSION_STATE_UNSPECIFIED,
			grpcproto.SessionState_SESSION_STATE_IDLE,
			grpcproto.SessionState_SESSION_STATE_CONFIGURED,
			grpcproto.SessionState_SESSION_STATE_PROBING,
			grpcproto.SessionState_SESSION_STATE_PREPARING,
			grpcproto.SessionState_SESSION_STATE_STOPPING:
			continue
		case grpcproto.SessionState_SESSION_STATE_CONNECTED:
			fmt.Println("CONNECTED")
			return exitOK, true
		case grpcproto.SessionState_SESSION_STATE_FAILED:
			return reportFailure(nil, snapshot.GetLastFailure()), false
		}
	}
}

func checkConfig(ctx context.Context, client grpcproto.VpnClient, source string) int {
	raw, err := readSource(source)
	if err != nil {
		reportCLIError("configuration source rejected", err)
		return exitArgs
	}
	result, err := client.ValidateConfig(ctx, &grpcproto.SessionValidateConfigRequest{RawConfig: raw})
	if err != nil || result == nil || result.GetFailure() != nil {
		return reportFailure(err, failureOf(result))
	} else {
		fmt.Printf("profiles=%d source=%s\n", len(result.GetProfiles()), result.GetSourceKind().String())
	}
	return exitOK
}

func profileInventory(source string) int {
	raw, err := readSource(source)
	if err != nil {
		reportCLIError("configuration source rejected", err)
		return exitArgs
	}
	profiles, err := sessionapi.InspectProfiles(raw)
	if err != nil {
		return reportFailure(err, nil)
	}
	encoded, err := profileInventoryJSON(profiles)
	if err != nil {
		return reportFailure(err, nil)
	}
	fmt.Println(string(encoded))
	return exitOK
}

func profileInventoryJSON(profiles []sessionapi.ProfileSummary) ([]byte, error) {
	type profileIdentity struct {
		Index    int32  `json:"index"`
		Protocol string `json:"protocol"`
	}
	type inventory struct {
		Profiles []profileIdentity `json:"profiles"`
	}
	values := make([]profileIdentity, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Index < 0 {
			return nil, fmt.Errorf("invalid profile identity")
		}
		protocol := string(profile.Protocol)
		switch profile.Protocol {
		case sessionapi.ProtocolOutline, sessionapi.ProtocolXray, sessionapi.ProtocolTrustTunnel:
		default:
			return nil, fmt.Errorf("invalid profile protocol")
		}
		values = append(values, profileIdentity{Index: profile.Index, Protocol: protocol})
	}
	return json.Marshal(inventory{Profiles: values})
}

func disconnect(ctx context.Context, client grpcproto.VpnClient) int {
	response, err := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{})
	if err != nil || response == nil || response.GetFailure() != nil {
		return reportFailure(err, failureOf(response))
	}
	current := response.GetSnapshot()
	if current == nil {
		return reportFailure(errors.New("service returned a snapshot response without a snapshot"), nil)
	}
	timedOut, stopFailure, stopErr := stopAndWaitForDisconnect(ctx, client, current)
	if timedOut {
		return reportFailure(stopErr, stopFailure)
	}
	if stopErr != nil || stopFailure != nil {
		return reportFailure(stopErr, stopFailure)
	}
	fmt.Println("DISCONNECTED")
	return exitOK
}

func stopAndWaitForDisconnect(
	ctx context.Context,
	client grpcproto.VpnClient,
	current *grpcproto.SessionSnapshot,
) (bool, *grpcproto.SessionFailure, error) {
	if current.GetGeneration() == 0 || current.GetCleanupComplete() {
		return false, cleanupFailure(current), nil
	}
	generation := current.GetGeneration()
	stopped, stopErr := client.Stop(ctx, &grpcproto.SessionStopRequest{
		SessionId: current.GetSessionId(), Generation: generation,
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
		current, getErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: current.GetSessionId()})
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
			if currentSnapshot.GetGeneration() == generation {
				return false, cleanupFailure(currentSnapshot), nil
			}
			return false, nil, nil
		}
		select {
		case <-ctx.Done():
			return true, nil, fmt.Errorf("wait for disconnect cleanup: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func status(ctx context.Context, client grpcproto.VpnClient, jsonOutput bool) int {
	response, err := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{})
	if err != nil {
		return reportFailure(err, nil)
	}
	if response == nil || response.GetFailure() != nil {
		return reportFailure(nil, failureOf(response))
	}
	snapshot := response.GetSnapshot()
	if snapshot == nil {
		return reportFailure(errors.New("service returned a snapshot response without a snapshot"), nil)
	}
	state, generation := snapshot.GetState(), snapshot.GetGeneration()
	if jsonOutput {
		code, label := publicStatus(state)
		encoded, encodeErr := json.Marshal(struct {
			Code  int    `json:"code"`
			State string `json:"state"`
		}{Code: code, State: label})
		if encodeErr != nil {
			return reportFailure(fmt.Errorf("encode status response: %w", encodeErr), nil)
		}
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
		grpcproto.SessionState_SESSION_STATE_FAILED:
	}
	return code, label
}

func verifySession(ctx context.Context, client grpcproto.VpnClient) int {
	return status(ctx, client, false)
}

func readSource(source string) ([]byte, error) {
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
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
		return nil, true, fmt.Errorf("source URL must use HTTPS")
	}
	return []byte(source), true, nil
}

func readSourceFileOrInline(source string) ([]byte, error) {
	cleanPath := filepath.Clean(source)
	if data, err := os.ReadFile(cleanPath); err == nil {
		return data, nil
	} else if !sourceFileReadMayFallbackToInline(err) {
		return nil, fmt.Errorf("cannot read configuration source: %w", err)
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

// cleanupSession stops only the generation started by this CLI invocation.
// It leaves the process-owned session and accepted configuration available.
func cleanupSession(client grpcproto.VpnClient, sessionID string, generation uint64) error {
	if generation == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, snapshotErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: sessionID})
	switch {
	case snapshotErr != nil:
		return fmt.Errorf("read cleanup session snapshot: %w", snapshotErr)
	case snapshot == nil:
		return errors.New("cleanup session snapshot response is nil")
	case snapshot.GetFailure() != nil:
		return sessionFailureError("read cleanup session snapshot", snapshot.GetFailure())
	}
	current := snapshot.GetSnapshot()
	if current == nil {
		return errors.New("cleanup session snapshot payload is nil")
	}
	if current.GetGeneration() != generation {
		return nil
	}
	if current.GetCleanupComplete() {
		if failure := cleanupFailure(current); failure != nil {
			return sessionFailureError("cleanup session", failure)
		}
		return nil
	}
	stopped, stopErr := client.Stop(ctx, &grpcproto.SessionStopRequest{
		SessionId: sessionID, Generation: generation,
	})
	if stopErr != nil {
		return fmt.Errorf("stop cleanup session: %w", stopErr)
	}
	if stopped == nil {
		return errors.New("stop cleanup session response is nil")
	}
	if stopped.GetFailure() != nil {
		return sessionFailureError("stop cleanup session", stopped.GetFailure())
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for cleanup session: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		currentSnapshot, snapshotErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: sessionID})
		if snapshotErr != nil {
			return fmt.Errorf("poll cleanup session snapshot: %w", snapshotErr)
		}
		if currentSnapshot == nil {
			return errors.New("poll cleanup session snapshot response is nil")
		}
		if currentSnapshot.GetFailure() != nil {
			return sessionFailureError("poll cleanup session snapshot", currentSnapshot.GetFailure())
		}
		if currentSnapshot.GetSnapshot() == nil {
			return errors.New("poll cleanup session snapshot payload is nil")
		}
		completed := currentSnapshot.GetSnapshot()
		if completed.GetGeneration() != generation {
			return nil
		}
		if completed.GetCleanupComplete() {
			if failure := cleanupFailure(completed); failure != nil {
				return sessionFailureError("wait for cleanup session", failure)
			}
			return nil
		}
	}
}

func cleanupFailure(snapshot *grpcproto.SessionSnapshot) *grpcproto.SessionFailure {
	failure := snapshot.GetLastFailure()
	if failure == nil || failure.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CLEANUP_FAILED {
		return nil
	}
	return failure
}

func externalIP() int {
	client := &http.Client{Timeout: 10 * time.Second}
	var failures []error
	for _, endpoint := range []string{"https://api.ipify.org", "https://ifconfig.me/ip"} {
		request, requestErr := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, http.NoBody)
		if requestErr != nil {
			failures = append(failures, fmt.Errorf("create external IP request: %w", requestErr))
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			failures = append(failures, fmt.Errorf("external IP request failed: %w", err))
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if closeErr != nil {
			failures = append(failures, fmt.Errorf("close external IP response: %w", closeErr))
			continue
		}
		if readErr != nil {
			failures = append(failures, fmt.Errorf("read external IP response: %w", readErr))
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			failures = append(failures, fmt.Errorf("external IP response status=%d body=%q", response.StatusCode, body))
			continue
		}
		value := strings.TrimSpace(string(body))
		if value == "" {
			failures = append(failures, errors.New("external IP response body is empty"))
			continue
		}
		fmt.Println(value)
		return exitOK
	}
	reportCLIError("external IP lookup failed", errors.Join(failures...))
	return exitRuntime
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
	if err != nil {
		if applicationlog.IsInitialized() {
			applicationlog.Error("CLI", "operation transport failed", map[string]any{
				"errorType": fmt.Sprintf("%T", err),
				"error":     err.Error(),
			})
		}
		fmt.Fprintf(os.Stderr, "dobby-cli: operation transport failed errorType=%T error=%v\n", err, err)
	}
	if failure != nil {
		if applicationlog.IsInitialized() {
			applicationlog.Error("CLI", "operation rejected", map[string]any{
				"failureCode":    failure.GetCode().String(),
				"failureMessage": failure.GetMessage(),
			})
		}
		fmt.Fprintf(
			os.Stderr,
			"dobby-cli: operation rejected failureCode=%s failureMessage=%q\n",
			failure.GetCode().String(),
			failure.GetMessage(),
		)
	}
	if err == nil && failure == nil {
		if applicationlog.IsInitialized() {
			applicationlog.Error("CLI", "operation failed because the service returned no result or diagnostic", nil)
		}
		fmt.Fprintln(os.Stderr, "dobby-cli: operation failed because the service returned no result or diagnostic")
	}
	if failure != nil && failure.GetCode() == grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT {
		return exitConflict
	}
	return exitRuntime
}

func sessionFailureError(operation string, failure *grpcproto.SessionFailure) error {
	if failure == nil {
		return fmt.Errorf("%s returned no failure diagnostic", operation)
	}
	return fmt.Errorf(
		"%s failed failureCode=%s failureMessage=%q",
		operation,
		failure.GetCode().String(),
		failure.GetMessage(),
	)
}

func reportCLIError(operation string, err error) {
	if applicationlog.IsInitialized() {
		fields := map[string]any{}
		if err != nil {
			fields["errorType"] = fmt.Sprintf("%T", err)
			fields["error"] = err.Error()
		}
		applicationlog.Error("CLI", operation, fields)
	}
	fmt.Fprintf(os.Stderr, "dobby-cli: %s errorType=%T error=%v\n", operation, err, err)
}

func usage(message string) int {
	fmt.Fprintln(os.Stderr, "dobby-cli:", message)
	printHelp()
	return exitArgs
}

func printHelp() {
	fmt.Println("dobby-cli connect <source> | connect-profile <source> <index> | profile-inventory <source> | check-config <source> | disconnect | status [--json] | logs clear | external-ip | verify-session")
}
