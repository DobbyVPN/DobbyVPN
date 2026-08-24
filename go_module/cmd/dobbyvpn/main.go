// Command dobby-cli is the native desktop operator client. It uses the
// authenticated SessionV2 control channel and never starts a JVM or a second
// VPN runtime.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
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
	defer conn.Close()
	client := grpcproto.NewVpnClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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
		index, parseErr := strconv.Atoi(args[2])
		if parseErr != nil || index < 0 {
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
	if runtime.GOOS == "windows" {
		if err := initWindowsServiceLogger(ctx, client, os.UserHomeDir); err != nil {
			fmt.Fprintln(os.Stderr, "dobby-cli: local service logging unavailable")
			return exitRuntime
		}
	} else if err := initOptInServiceLogger(ctx, client); err != nil {
		fmt.Fprintln(os.Stderr, "dobby-cli: local service logging unavailable")
		return exitRuntime
	}
	created, err := client.CreateSession(ctx, &grpcproto.SessionCreateSessionRequest{})
	if err != nil || created == nil || created.GetFailure() != nil {
		return reportFailure(err, failureOf(created))
	}
	sessionID := created.GetSessionId()
	keepSession := false
	var startedGeneration uint64
	defer func() {
		if !keepSession {
			cleanupSession(client, sessionID, startedGeneration)
		}
	}()
	configured, err := client.Configure(ctx, &grpcproto.SessionConfigureRequest{
		SessionId: sessionID, CommandId: commandID("configure"), RawConfig: raw,
	})
	if err != nil || configured == nil || configured.GetFailure() != nil {
		return reportFailure(err, failureOf(configured))
	}
	start := &grpcproto.SessionStartRequest{SessionId: sessionID, CommandId: commandID("start")}
	if profileIndex == nil {
		start.Mode = grpcproto.SessionStartMode_SESSION_START_MODE_AUTO_SELECT
	} else {
		start.Mode = grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX
		start.ProfileIndex = int32(*profileIndex)
	}
	started, err := client.Start(ctx, start)
	if err != nil || started == nil || started.GetFailure() != nil {
		return reportFailure(err, failureOf(started))
	}
	startedGeneration = started.GetGeneration()
	stream, err := client.Watch(ctx, &grpcproto.SessionObserveRequest{SessionId: sessionID})
	if err != nil {
		return reportFailure(err, nil)
	}
	for {
		event, recvErr := stream.Recv()
		if recvErr == io.EOF {
			return exitRuntime
		}
		if recvErr != nil {
			return reportFailure(recvErr, nil)
		}
		switch event.GetState() {
		case grpcproto.SessionState_SESSION_STATE_CONNECTED:
			keepSession = true
			fmt.Println("CONNECTED")
			return exitOK
		case grpcproto.SessionState_SESSION_STATE_FAILED:
			fmt.Fprintln(os.Stderr, "dobby-cli: tunnel did not connect")
			return exitRuntime
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
	defer func() {
		_, _ = client.DestroySession(context.Background(), &grpcproto.SessionDestroySessionRequest{SessionId: id})
	}()
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
		if recovered.GetFailure().GetCode() == grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_NOT_FOUND {
			fmt.Println("DISCONNECTED")
			return exitOK
		}
		return reportFailure(nil, failureOf(recovered))
	}
	id := recovered.GetSessionId()
	snapshot, err := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: id})
	if err != nil || snapshot == nil || snapshot.GetFailure() != nil {
		return reportFailure(err, failureOf(snapshot))
	}
	if generation := snapshot.GetSnapshot().GetGeneration(); generation != 0 && !snapshot.GetSnapshot().GetCleanupComplete() {
		stopped, stopErr := client.Stop(ctx, &grpcproto.SessionStopRequest{SessionId: id, CommandId: commandID("stop"), Generation: generation})
		if stopErr != nil || stopped == nil || stopped.GetFailure() != nil {
			return reportFailure(stopErr, failureOf(stopped))
		}
		for {
			current, getErr := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: id})
			if getErr != nil || current == nil || current.GetFailure() != nil {
				return reportFailure(getErr, failureOf(current))
			}
			if current.GetSnapshot().GetCleanupComplete() {
				break
			}
			select {
			case <-ctx.Done():
				return exitRuntime
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if _, err := client.DestroySession(ctx, &grpcproto.SessionDestroySessionRequest{SessionId: id}); err != nil {
		return reportFailure(err, nil)
	}
	fmt.Println("DISCONNECTED")
	return exitOK
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
func publicStatus(state grpcproto.SessionState) (int, string) {
	switch state {
	case grpcproto.SessionState_SESSION_STATE_PROBING,
		grpcproto.SessionState_SESSION_STATE_PREPARING,
		grpcproto.SessionState_SESSION_STATE_CONFIGURED,
		grpcproto.SessionState_SESSION_STATE_STOPPING:
		return 1, "Connecting"
	case grpcproto.SessionState_SESSION_STATE_CONNECTED:
		return 2, "Connected"
	default:
		return 0, "Disconnected"
	}
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
	if !isWindowsPath(source) {
		if parsed, err := url.Parse(source); err == nil && parsed.Scheme != "" {
			if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
				return nil, fmt.Errorf("source URL must use HTTP or HTTPS")
			}
			if len(source) > maxSource {
				return nil, fmt.Errorf("source too large")
			}
			return []byte(source), nil
		}
	}
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
	if snapshot, err := client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: sessionID}); err == nil && snapshot != nil && snapshot.GetFailure() == nil {
		current := snapshot.GetSnapshot()
		if current != nil && generation == 0 {
			generation = current.GetGeneration()
		}
		if current != nil && !current.GetCleanupComplete() && generation != 0 {
			_, _ = client.Stop(ctx, &grpcproto.SessionStopRequest{SessionId: sessionID, CommandId: commandID("cleanup-stop"), Generation: generation})
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
					break
				}
			}
		}
	}
	_, _ = client.DestroySession(ctx, &grpcproto.SessionDestroySessionRequest{SessionId: sessionID})
}

func externalIP() int {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, endpoint := range []string{"https://api.ipify.org", "https://ifconfig.me/ip"} {
		request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 256))
		response.Body.Close()
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
