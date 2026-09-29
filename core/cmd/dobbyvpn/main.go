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

	"core/clientserver/controljson"
	applicationlog "core/log"
	"core/sessionapi"
)

const (
	exitOK                   = 0
	exitArgs                 = 2
	exitConnect              = 3
	exitRuntime              = 4
	exitConflict             = 8
	sessionIDRequiredMessage = "--session-id requires one value"
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
	client := dialService()
	timeout := 30 * time.Second
	if args[0] == "start" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
	case "configure":
		if len(args) != 2 {
			return usage("configure requires a source")
		}
		return configureJSON(ctx, client, args[1])
	case "start":
		return startJSON(ctx, client, args[1:])
	case "stop":
		return stopJSON(ctx, client, args[1:])
	case "snapshot":
		if len(args) != 1 {
			return usage("snapshot accepts no options")
		}
		return snapshotJSON(ctx, client)
	case "external-ip":
		return externalIP()
	default:
		return usage("unknown command")
	}
}

type jsonResponse struct {
	OK     bool            `json:"ok"`
	Result any             `json:"result,omitempty"`
	Error  *controlFailure `json:"error,omitempty"`
}

func writeJSONResult(result any) int {
	if err := json.NewEncoder(os.Stdout).Encode(jsonResponse{OK: true, Result: result}); err != nil {
		return reportFailure(fmt.Errorf("encode CLI response: %w", err))
	}
	return exitOK
}

func writeJSONFailure(err error) int {
	if err == nil {
		err = errors.New("desktop backend returned no failure details")
	}
	code := string(sessionapi.FailureInternal)
	message := err.Error()
	var remote *controljson.CallError
	if errors.As(err, &remote) {
		code, message = remote.Code, remote.Message
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(jsonResponse{
		Error: &controlFailure{Code: code, Message: message},
	}); encodeErr != nil {
		reportCLIError("encode CLI failure", encodeErr)
	}
	return reportFailure(err)
}

func configureJSON(ctx context.Context, client controljson.Client, source string) int {
	raw, err := readSource(source)
	if err != nil {
		return writeJSONFailure(&controljson.CallError{
			Code: string(sessionapi.FailureInvalidArgument), Message: "configuration source rejected: " + err.Error(),
		})
	}
	current, err := callSnapshot(ctx, client, "")
	if err != nil {
		return writeJSONFailure(err)
	}
	var configured struct {
		Digest     string           `json:"digest"`
		Sequence   uint64           `json:"sequence"`
		Profiles   []controlProfile `json:"profiles"`
		SourceKind string           `json:"source_kind"`
	}
	err = client.Call(ctx, "Configure", struct {
		SessionID        string `json:"session_id"`
		ExpectedSequence uint64 `json:"expected_sequence"`
		Source           string `json:"source"`
	}{current.SessionID, current.Sequence, string(raw)}, &configured)
	if err != nil {
		return writeJSONFailure(err)
	}
	return writeJSONResult(struct {
		SessionID  string           `json:"session_id"`
		Digest     string           `json:"digest"`
		Sequence   uint64           `json:"sequence"`
		Profiles   []controlProfile `json:"profiles"`
		SourceKind string           `json:"source_kind"`
	}{current.SessionID, configured.Digest, configured.Sequence, configured.Profiles, configured.SourceKind})
}

type startOptions struct {
	mode      string
	index     int32
	sessionID string
	digest    string
}

func parseStartOptions(args []string) (options startOptions, errorMessage string) {
	options = startOptions{mode: string(sessionapi.AutoSelect)}
	selectionSet := false
	for len(args) > 0 {
		option := args[0]
		args = args[1:]
		switch option {
		case "--auto":
			if selectionSet {
				return options, "choose either --auto or --profile"
			}
			selectionSet = true
		case "--profile":
			if selectionSet {
				return options, "--profile requires one index"
			}
			value, remaining, ok := takeOptionValue(args)
			if !ok {
				return options, "--profile requires one index"
			}
			args = remaining
			index, err := parseProfileIndex(value)
			if err != nil {
				return options, err.Error()
			}
			options.index = index
			options.mode = string(sessionapi.ProfileIndex)
			selectionSet = true
		case "--session-id":
			if options.sessionID != "" {
				return options, sessionIDRequiredMessage
			}
			value, remaining, ok := takeOptionValue(args)
			if !ok {
				return options, sessionIDRequiredMessage
			}
			options.sessionID = value
			args = remaining
		case "--config-digest":
			if options.digest != "" {
				return options, "--config-digest requires one value"
			}
			value, remaining, ok := takeOptionValue(args)
			if !ok {
				return options, "--config-digest requires one value"
			}
			options.digest = value
			args = remaining
		default:
			return options, "unknown start option"
		}
	}
	if !selectionSet || (options.sessionID == "") != (options.digest == "") {
		return options, "start requires --auto or --profile and paired --session-id/--config-digest guards"
	}
	return options, ""
}

func startJSON(ctx context.Context, client controljson.Client, args []string) int {
	options, parseError := parseStartOptions(args)
	if parseError != "" {
		return usage(parseError)
	}
	current, err := callSnapshot(ctx, client, "")
	if err != nil {
		return writeJSONFailure(err)
	}
	if options.sessionID != "" && (current.SessionID != options.sessionID || current.Digest != options.digest) {
		return writeJSONFailure(&controljson.CallError{Code: string(sessionapi.FailureConflict), Message: "accepted configuration changed; configure and select again"})
	}
	if !current.Configured {
		return writeJSONFailure(&controljson.CallError{Code: string(sessionapi.FailureNotConfigured), Message: "configure a session before starting it"})
	}
	var started controlResult
	err = client.Call(ctx, "Start", struct {
		SessionID        string `json:"session_id"`
		ExpectedSequence uint64 `json:"expected_sequence"`
		Mode             string `json:"mode"`
		Index            int32  `json:"index"`
	}{current.SessionID, current.Sequence, options.mode, options.index}, &started)
	if err != nil {
		return writeJSONFailure(err)
	}
	return waitForConnection(ctx, client, current.SessionID, started)
}

func waitForConnection(ctx context.Context, client controljson.Client, sessionID string, started controlResult) int {
	connected := false
	defer func() {
		if !connected {
			if cleanupErr := cleanupSession(client, sessionID, started.Generation); cleanupErr != nil {
				reportCLIError("failed connection session cleanup failed", cleanupErr)
			}
		}
	}()
	for {
		snapshot, snapshotErr := callSnapshot(ctx, client, sessionID)
		if snapshotErr != nil {
			return writeJSONFailure(snapshotErr)
		}
		switch snapshot.State {
		case string(sessionapi.StateConnected):
			connected = true
			return writeJSONResult(snapshot)
		case string(sessionapi.StateFailed):
			return writeJSONFailure(failureError(snapshot.LastFailure))
		}
		select {
		case <-ctx.Done():
			return writeJSONFailure(fmt.Errorf("wait for connection: %w", ctx.Err()))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func takeOptionValue(args []string) (value string, remaining []string, found bool) {
	if len(args) == 0 {
		return "", args, false
	}
	return args[0], args[1:], true
}

type stopOptions struct {
	sessionID  string
	generation uint64
}

func parseStopOptions(args []string) (options stopOptions, errorMessage string) {
	options = stopOptions{}
	for len(args) > 0 {
		option := args[0]
		args = args[1:]
		switch option {
		case "--session-id":
			if options.sessionID != "" {
				return options, sessionIDRequiredMessage
			}
			value, remaining, ok := takeOptionValue(args)
			if !ok {
				return options, sessionIDRequiredMessage
			}
			options.sessionID = value
			args = remaining
		case "--generation":
			if options.generation != 0 {
				return options, "--generation requires one value"
			}
			value, remaining, ok := takeOptionValue(args)
			if !ok {
				return options, "--generation requires one value"
			}
			generation, err := strconv.ParseUint(value, 10, 64)
			if err != nil || generation == 0 {
				return options, "--generation must be a positive integer"
			}
			options.generation = generation
			args = remaining
		default:
			return options, "unknown stop option"
		}
	}
	if (options.sessionID == "") != (options.generation == 0) {
		return options, "stop requires paired --session-id/--generation guards"
	}
	return options, ""
}

func stopJSON(ctx context.Context, client controljson.Client, args []string) int {
	options, parseError := parseStopOptions(args)
	if parseError != "" {
		return usage(parseError)
	}
	snapshot, err := callSnapshot(ctx, client, "")
	if err != nil {
		return writeJSONFailure(err)
	}
	if options.sessionID != "" && (snapshot.SessionID != options.sessionID || snapshot.Generation != options.generation) {
		return writeJSONFailure(&controljson.CallError{Code: string(sessionapi.FailureConflict), Message: "session generation changed; refresh its snapshot before stopping"})
	}
	if snapshot.Generation == 0 || snapshot.CleanupComplete {
		if err := cleanupFailure(&snapshot); err != nil {
			return writeJSONFailure(err)
		}
		return writeJSONResult(snapshot)
	}
	var stopped controlResult
	if err := client.Call(ctx, "Stop", struct {
		SessionID  string `json:"session_id"`
		Generation uint64 `json:"generation"`
	}{snapshot.SessionID, snapshot.Generation}, &stopped); err != nil {
		return writeJSONFailure(err)
	}
	return waitForDisconnect(ctx, client, snapshot.SessionID)
}

func waitForDisconnect(ctx context.Context, client controljson.Client, sessionID string) int {
	for {
		current, err := callSnapshot(ctx, client, sessionID)
		if err != nil {
			return writeJSONFailure(err)
		}
		if current.CleanupComplete {
			if err := cleanupFailure(&current); err != nil {
				return writeJSONFailure(err)
			}
			return writeJSONResult(current)
		}
		select {
		case <-ctx.Done():
			return writeJSONFailure(fmt.Errorf("wait for disconnect cleanup: %w", ctx.Err()))
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func snapshotJSON(ctx context.Context, client controljson.Client) int {
	snapshot, err := callSnapshot(ctx, client, "")
	if err != nil {
		return writeJSONFailure(err)
	}
	return writeJSONResult(snapshot)
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
	return &controljson.CallError{Code: failure.Code, Message: failure.Message}
}

func readSource(source string) ([]byte, error) {
	sourceURL, isURL, err := parseSourceURL(source)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration URL: %w", err)
	}
	if isURL {
		return sourceURL, nil
	}
	cleanPath := filepath.Clean(source)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read configuration file: %w", err)
	}
	return data, nil
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
		if remote.Code == string(sessionapi.FailureConflict) || remote.Code == string(sessionapi.FailureInvalidArgument) {
			fmt.Fprintf(os.Stderr, "dobby-cli: operation rejected failureCode=%s failureMessage=%q\n", remote.Code, remote.Message)
			if remote.Code == string(sessionapi.FailureConflict) {
				return exitConflict
			}
			return exitArgs
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
	fmt.Println("dobby-cli configure <file-or-https-url> | start (--auto | --profile <index>) [--session-id <id> --config-digest <digest>] | stop [--session-id <id> --generation <number>] | snapshot | logs clear | external-ip")
}
