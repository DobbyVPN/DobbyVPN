// Command dobby-cli operates the process-owned desktop Go backend.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go_module/desktop_exports/controljson"
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

type controlFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type controlSnapshot struct {
	SessionID       string           `json:"session_id"`
	Sequence        uint64           `json:"sequence"`
	Generation      uint64           `json:"generation"`
	State           string           `json:"state"`
	Configured      bool             `json:"configured"`
	Digest          string           `json:"digest"`
	SourceKind      string           `json:"source_kind"`
	SourceURL       string           `json:"source_url"`
	SourceError     string           `json:"source_error"`
	Profiles        []controlProfile `json:"profiles"`
	Warnings        []controlWarning `json:"warnings"`
	ActiveProfile   *controlProfile  `json:"active_profile"`
	LastFailure     *controlFailure  `json:"last_failure"`
	CleanupComplete bool             `json:"cleanup_complete"`
	Recovering      bool             `json:"recovering"`
}

type controlProfile struct {
	Index       int32  `json:"index"`
	Protocol    string `json:"protocol"`
	Description string `json:"description"`
}

type controlWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type controlResult struct {
	Sequence   uint64 `json:"sequence"`
	Generation uint64 `json:"generation"`
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if isHelpCommand(args) {
		printHelp()
		return exitOK
	}
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
	client := dialService()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runServiceCommand(ctx, client, args)
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

func runServiceCommand(ctx context.Context, client controljson.Client, args []string) int {
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
		index, err := parseProfileIndex(args[2])
		if err != nil {
			reportCLIError("profile index rejected", err)
			return exitArgs
		}
		return connect(ctx, client, args[1], &index)
	case "check-config":
		if len(args) != 2 {
			return usage("check-config requires a config path, URL, or inline TOML")
		}
		return checkConfig(ctx, args[1])
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
		return status(ctx, client, false)
	default:
		return usage("unknown command")
	}
}

func callSnapshot(ctx context.Context, client controljson.Client, sessionID string) (controlSnapshot, error) {
	var snapshot controlSnapshot
	err := client.Call(ctx, "Snapshot", struct {
		SessionID string `json:"session_id"`
	}{SessionID: sessionID}, &snapshot)
	return snapshot, err
}

func parseProfileIndex(value string) (int32, error) {
	index, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse profile index: %w", err)
	}
	if index < 0 {
		return 0, fmt.Errorf("profile index must be non-negative")
	}
	return int32(index), nil
}

func applicationLogPath(home string) string {
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
	if err := clearLocalLogFileAtBase(applicationLogPath(home), home); err != nil {
		fmt.Fprintf(os.Stderr, "dobby-cli: local application log clear failed: %v\n", err)
		return exitRuntime
	}
	fmt.Println("LOGS_CLEARED")
	return exitOK
}

func connect(ctx context.Context, client controljson.Client, source string, profileIndex *int32) int {
	raw, err := readSource(source)
	if err != nil {
		reportCLIError("configuration source rejected", err)
		return exitArgs
	}
	current, err := callSnapshot(ctx, client, "")
	if err != nil {
		return reportFailure(err)
	}
	configured := struct {
		Sequence uint64 `json:"sequence"`
	}{}
	err = client.Call(ctx, "Configure", struct {
		SessionID        string `json:"session_id"`
		ExpectedSequence uint64 `json:"expected_sequence"`
		Source           string `json:"source"`
	}{current.SessionID, current.Sequence, string(raw)}, &configured)
	if err != nil {
		return reportFailure(err)
	}
	started := controlResult{}
	mode, index := string(sessionapi.AutoSelect), int32(0)
	if profileIndex != nil {
		mode, index = string(sessionapi.ProfileIndex), *profileIndex
	}
	err = client.Call(ctx, "Start", struct {
		SessionID        string `json:"session_id"`
		ExpectedSequence uint64 `json:"expected_sequence"`
		Mode             string `json:"mode"`
		Index            int32  `json:"index"`
	}{current.SessionID, configured.Sequence, mode, index}, &started)
	if err != nil {
		return reportFailure(err)
	}
	connected := false
	defer func() {
		if !connected {
			if cleanupErr := cleanupSession(client, current.SessionID, started.Generation); cleanupErr != nil {
				reportCLIError("failed connection session cleanup failed", cleanupErr)
			}
		}
	}()
	for {
		snapshot, snapshotErr := callSnapshot(ctx, client, current.SessionID)
		if snapshotErr != nil {
			return reportFailure(snapshotErr)
		}
		switch snapshot.State {
		case string(sessionapi.StateConnected):
			connected = true
			fmt.Println("CONNECTED")
			return exitOK
		case string(sessionapi.StateFailed):
			return reportFailure(failureError(snapshot.LastFailure))
		}
		select {
		case <-ctx.Done():
			return reportFailure(fmt.Errorf("wait for connection: %w", ctx.Err()))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func checkConfig(ctx context.Context, source string) int {
	raw, err := readSource(source)
	if err != nil {
		reportCLIError("configuration source rejected", err)
		return exitArgs
	}
	manager := sessionapi.NewManager(sessionapi.ManagerOptions{})
	result, err := manager.ValidateConfig(ctx, raw)
	if err != nil {
		return reportFailure(err)
	}
	fmt.Printf("profiles=%d source=%s\n", len(result.Profiles), result.SourceKind)
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
		return reportFailure(err)
	}
	type profileIdentity struct {
		Index    int32  `json:"index"`
		Protocol string `json:"protocol"`
	}
	values := make([]profileIdentity, 0, len(profiles))
	for _, profile := range profiles {
		values = append(values, profileIdentity{Index: profile.Index, Protocol: string(profile.Protocol)})
	}
	encoded, err := json.Marshal(struct {
		Profiles []profileIdentity `json:"profiles"`
	}{values})
	if err != nil {
		return reportFailure(err)
	}
	fmt.Println(string(encoded))
	return exitOK
}

func disconnect(ctx context.Context, client controljson.Client) int {
	snapshot, err := callSnapshot(ctx, client, "")
	if err != nil {
		return reportFailure(err)
	}
	if snapshot.Generation == 0 || snapshot.CleanupComplete {
		if err := cleanupFailure(&snapshot); err != nil {
			return reportFailure(err)
		}
		fmt.Println("DISCONNECTED")
		return exitOK
	}
	var stopped controlResult
	if err := client.Call(ctx, "Stop", struct {
		SessionID  string `json:"session_id"`
		Generation uint64 `json:"generation"`
	}{snapshot.SessionID, snapshot.Generation}, &stopped); err != nil {
		return reportFailure(err)
	}
	for {
		current, err := callSnapshot(ctx, client, snapshot.SessionID)
		if err != nil {
			return reportFailure(err)
		}
		if current.CleanupComplete {
			if err := cleanupFailure(&current); err != nil {
				return reportFailure(err)
			}
			fmt.Println("DISCONNECTED")
			return exitOK
		}
		select {
		case <-ctx.Done():
			return reportFailure(fmt.Errorf("wait for disconnect cleanup: %w", ctx.Err()))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func status(ctx context.Context, client controljson.Client, jsonOutput bool) int {
	snapshot, err := callSnapshot(ctx, client, "")
	if err != nil {
		return reportFailure(err)
	}
	code, label := publicStatus(snapshot.State)
	if jsonOutput {
		encoded, err := json.Marshal(struct {
			Code  int    `json:"code"`
			State string `json:"state"`
		}{code, label})
		if err != nil {
			return reportFailure(fmt.Errorf("encode status response: %w", err))
		}
		fmt.Println(string(encoded))
	} else {
		fmt.Printf("state=%s generation=%d\n", snapshot.State, snapshot.Generation)
	}
	return exitOK
}

func publicStatus(state string) (code int, label string) {
	switch sessionapi.State(state) {
	case sessionapi.StateConfigured, sessionapi.StateProbing, sessionapi.StatePreparing, sessionapi.StateStopping:
		return 1, "Connecting"
	case sessionapi.StateConnected:
		return 2, "Connected"
	default:
		return 0, "Disconnected"
	}
}

func cleanupSession(client controljson.Client, sessionID string, generation uint64) error {
	if generation == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	current, err := callSnapshot(ctx, client, sessionID)
	if err != nil {
		return fmt.Errorf("read cleanup session snapshot: %w", err)
	}
	if current.Generation != generation || current.CleanupComplete {
		return cleanupFailure(&current)
	}
	var stopped controlResult
	if err := client.Call(ctx, "Stop", struct {
		SessionID  string `json:"session_id"`
		Generation uint64 `json:"generation"`
	}{sessionID, generation}, &stopped); err != nil {
		return fmt.Errorf("stop cleanup session: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for cleanup session: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		current, err := callSnapshot(ctx, client, sessionID)
		if err != nil {
			return fmt.Errorf("poll cleanup session snapshot: %w", err)
		}
		if current.Generation != generation {
			return nil
		}
		if current.CleanupComplete {
			return cleanupFailure(&current)
		}
	}
}

func cleanupFailure(snapshot *controlSnapshot) error {
	if snapshot.LastFailure != nil && snapshot.LastFailure.Code == string(sessionapi.FailureCleanup) {
		return failureError(snapshot.LastFailure)
	}
	return nil
}

func failureError(failure *controlFailure) error {
	if failure == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", failure.Code, failure.Message)
}

func readSource(source string) ([]byte, error) {
	if sourceURL, isURL, err := parseSourceURL(source); isURL {
		if err != nil {
			return nil, err
		}
		return sourceURL, nil
	}
	cleanPath := filepath.Clean(source)
	if data, err := os.ReadFile(cleanPath); err == nil {
		return data, nil
	} else if !sourceFileReadMayFallbackToInline(err) {
		return nil, fmt.Errorf("cannot read configuration source: %w", err)
	}
	return []byte(source), nil
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

func isWindowsPath(source string) bool {
	if len(source) < 3 || source[1] != ':' {
		return false
	}
	letter := source[0]
	return (letter >= 'A' && letter <= 'Z' || letter >= 'a' && letter <= 'z') && (source[2] == '\\' || source[2] == '/')
}

func externalIP() int {
	client := &http.Client{Timeout: 10 * time.Second}
	var failures []error
	for _, endpoint := range []string{"https://api.ipify.org", "https://ifconfig.me/ip"} {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, http.NoBody)
		if err != nil {
			failures = append(failures, fmt.Errorf("create external IP request: %w", err))
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
		if value != "" {
			fmt.Println(value)
			return exitOK
		}
		failures = append(failures, errors.New("external IP response body is empty"))
	}
	reportCLIError("external IP lookup failed", errors.Join(failures...))
	return exitRuntime
}

func reportFailure(err error) int {
	if err == nil {
		err = errors.New("desktop backend returned no result")
	}
	var remote *controljson.CallError
	if errors.As(err, &remote) {
		if remote.Code == string(sessionapi.FailureConflict) {
			fmt.Fprintf(os.Stderr, "dobby-cli: operation rejected failureCode=%s failureMessage=%q\n", remote.Code, remote.Message)
			return exitConflict
		}
	}
	if applicationlog.IsInitialized() {
		applicationlog.Error("CLI", "operation failed", map[string]any{"errorType": fmt.Sprintf("%T", err), "error": err.Error()})
	}
	fmt.Fprintf(os.Stderr, "dobby-cli: operation failed errorType=%T error=%v\n", err, err)
	return exitRuntime
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
