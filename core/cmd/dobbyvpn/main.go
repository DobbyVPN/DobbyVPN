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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"core/sessionapi/wire"

	"core/clientserver/controljson"
	"core/diagnostics"
	applicationlog "core/log"
	"core/publicip"
	"core/sessionapi"
)

// Exit codes. Scripts may rely on these values.
const (
	exitOK = 0
	// exitArgs: wrong command line or a rejected configuration source.
	exitArgs = 2
	// exitConnect: the Dobby VPN service could not be reached.
	exitConnect = 3
	// exitRuntime: the operation failed (including a timeout).
	exitRuntime = 4
	// exitConflict: the accepted configuration or session generation changed.
	exitConflict             = 8
	stderrLabel              = "stderr"
	sessionIDRequiredMessage = "--session-id requires one value"
)

func main() {
	code := run(os.Args[1:])
	_ = applicationlog.Flush()
	os.Exit(code)
}

func run(args []string) int {
	if isHelpCommand(args) {
		if len(args) > 1 {
			return usage("help accepts no arguments")
		}
		printHelp()
		return exitOK
	}
	// Reject malformed commands before any side effect: no upload
	// cleanup, log file or service connection for a typo.
	var execute serviceCommand
	var message string
	if args[0] == "logs" {
		message = logsArgsError(args[1:])
	} else {
		execute, message = parseServiceCommand(args)
	}
	if message != "" {
		return usage(message)
	}
	if err := cleanupAbandonedUploads(); err != nil {
		fmt.Fprintf(os.Stderr, "dobby-cli: upload temporary cleanup: %v\n", err)
	}
	if args[0] == "logs" {
		if err := prepareCLIHistory(); err != nil {
			return reportFailure(err)
		}
		return runLogs(args[1:])
	}
	if err := initApplicationLogger(); err != nil {
		fmt.Fprintf(os.Stderr, "dobby-cli: application logging unavailable errorType=%T error=%v\n", err, err)
		return exitRuntime
	}
	applicationlog.Info("CLI", "dobby-cli command started", map[string]any{"command": args[0]})
	client := dialService()
	timeout := 30 * time.Second
	if args[0] == startCommand {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return execute(ctx, client)
}

func initApplicationLogger() error {
	if err := prepareCLIHistory(); err != nil {
		return err
	}
	path, err := cliLogPath()
	if err != nil {
		return err
	}
	return applicationlog.SetPath(path)
}

func prepareCLIHistory() error {
	path, err := cliLogPath()
	if err != nil {
		return err
	}
	var sources []diagnostics.LegacySource
	if runtime.GOOS == darwinPlatform || runtime.GOOS == "windows" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		legacy := applicationLogPath(home)
		sources = append(sources, diagnostics.LegacySource{Path: legacy, Label: "cli"}, diagnostics.LegacySource{Path: legacy + ".stderr", Label: stderrLabel})
	} else {
		sources = append(sources, diagnostics.LegacySource{Path: path + ".stderr", Label: stderrLabel})
	}
	if runtime.GOOS != "windows" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		legacyBackend := filepath.Join(base, "DobbyVPN", "Logs", "backend.jsonl")
		selectedBackend := os.Getenv("DOBBY_LOG_PATH")
		if filepath.Clean(legacyBackend) != filepath.Clean(path) && (selectedBackend == "" || filepath.Clean(legacyBackend) != filepath.Clean(selectedBackend)) {
			sources = append(sources, diagnostics.LegacySource{Path: legacyBackend, Label: "backend"}, diagnostics.LegacySource{Path: legacyBackend + ".stderr", Label: stderrLabel})
		}
	}
	filtered := sources[:0]
	for _, source := range sources {
		if filepath.Clean(source.Path) != filepath.Clean(path) {
			filtered = append(filtered, source)
		}
	}
	sources = filtered
	if err := diagnostics.MigrateLegacy(path, sources); err != nil {
		return err
	}
	return nil
}

func isHelpCommand(args []string) bool {
	return len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help"
}

type serviceCommand func(context.Context, controljson.Client) int

func parseServiceCommand(args []string) (run serviceCommand, usageMessage string) {
	switch args[0] {
	case "configure":
		if len(args) != 2 {
			return nil, "configure requires a source"
		}
		return func(ctx context.Context, client controljson.Client) int { return configureJSON(ctx, client, args[1]) }, ""
	case startCommand:
		options, message := parseStartOptions(args[1:])
		return options.start, message
	case "stop":
		options, message := parseStopOptions(args[1:])
		return options.stop, message
	case "snapshot":
		if len(args) != 1 {
			return nil, "snapshot accepts no options"
		}
		return snapshotJSON, ""
	case "external-ip":
		if len(args) != 1 {
			return nil, "external-ip accepts no options"
		}
		return func(context.Context, controljson.Client) int { return externalIP() }, ""
	default:
		return nil, "unknown command"
	}
}

func writeJSONResult(result any) int {
	if err := json.NewEncoder(os.Stdout).Encode(wire.Response[any]{OK: true, Result: result}); err != nil {
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
	var unavailable *serviceUnavailableError
	if errors.As(err, &remote) {
		code, message = remote.Code, remote.Message
	} else if errors.As(err, &unavailable) {
		code = failureServiceUnavailable
	}
	if encodeErr := json.NewEncoder(os.Stdout).Encode(wire.Response[any]{
		Error: &wire.Failure{Code: code, Message: message},
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
	var configured wire.Configuration
	err = client.Call(ctx, "Configure", struct {
		SessionID        string `json:"session_id"`
		ExpectedSequence uint64 `json:"expected_sequence"`
		Source           string `json:"source"`
	}{current.SessionID, current.Sequence, string(raw)}, &configured)
	if err != nil {
		return writeJSONFailure(err)
	}
	return writeJSONResult(struct {
		SessionID string `json:"session_id"`
		wire.Configuration
	}{current.SessionID, configured})
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
			if !ok || value == "" {
				return options, sessionIDRequiredMessage
			}
			options.sessionID = value
			args = remaining
		case "--config-digest":
			if options.digest != "" {
				return options, "--config-digest requires one value"
			}
			value, remaining, ok := takeOptionValue(args)
			if !ok || value == "" {
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

func (options startOptions) start(ctx context.Context, client controljson.Client) int {
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
	var started wire.Generation
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

func waitForConnection(ctx context.Context, client controljson.Client, sessionID string, started wire.Generation) int {
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
			if !ok || value == "" {
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

func (options stopOptions) stop(ctx context.Context, client controljson.Client) int {
	snapshot, err := callSnapshot(ctx, client, "")
	if err != nil {
		return writeJSONFailure(err)
	}
	if options.sessionID != "" && (snapshot.SessionID != options.sessionID || snapshot.Generation != options.generation) {
		return writeJSONFailure(&controljson.CallError{Code: string(sessionapi.FailureConflict), Message: "session generation changed; refresh its snapshot before stopping"})
	}
	if snapshot.Generation == 0 || (snapshot.CleanupComplete && snapshot.PrimaryAction != "STOP") {
		if cleanupErr := cleanupFailure(&snapshot); cleanupErr != nil {
			return writeJSONFailure(cleanupErr)
		}
		return writeJSONResult(snapshot)
	}
	stopped, err := callStop(ctx, client, snapshot.SessionID, snapshot.Generation)
	if err != nil {
		return writeJSONFailure(err)
	}
	return waitForDisconnect(ctx, client, snapshot.SessionID, stopped.Generation)
}

func callStop(ctx context.Context, client controljson.Client, sessionID string, generation uint64) (wire.Generation, error) {
	var stopped wire.Generation
	err := client.Call(ctx, "Stop", struct {
		SessionID  string `json:"session_id"`
		Generation uint64 `json:"generation"`
	}{sessionID, generation}, &stopped)
	return stopped, err
}

func waitForDisconnect(ctx context.Context, client controljson.Client, sessionID string, generation uint64) int {
	for {
		current, err := callSnapshot(ctx, client, sessionID)
		if err != nil {
			return writeJSONFailure(err)
		}
		if current.Generation != generation {
			return writeJSONFailure(&controljson.CallError{Code: string(sessionapi.FailureConflict), Message: "session restarted while waiting for stop cleanup; refresh its snapshot"})
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

func callSnapshot(ctx context.Context, client controljson.Client, sessionID string) (wire.Snapshot, error) {
	var snapshot wire.Snapshot
	err := client.Call(ctx, "Snapshot", struct {
		SessionID string `json:"session_id"`
	}{SessionID: sessionID}, &snapshot)
	if err == nil && snapshot.ProtocolVersion != wire.ProtocolVersion {
		// A service that predates the field reports 0, which is also a mismatch.
		err = &controljson.CallError{Code: string(sessionapi.FailureConflict), Message: wire.RestartMessage}
	}
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
	paths, err := diagnosticPaths()
	if err == nil {
		err = saveViewBoundary(paths)
	}
	if err != nil {
		return reportFailure(fmt.Errorf("clear diagnostic view: %w", err))
	}
	fmt.Println("LOGS_CLEARED")
	return exitOK
}

func cleanupSession(client controljson.Client, sessionID string, generation uint64) error {
	if generation == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionapi.CleanupTimeout)
	defer cancel()
	current, err := callSnapshot(ctx, client, sessionID)
	if err != nil {
		return fmt.Errorf("read cleanup session snapshot: %w", err)
	}
	if current.Generation != generation || current.CleanupComplete {
		return cleanupFailure(&current)
	}
	if _, err := callStop(ctx, client, sessionID, generation); err != nil {
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

func cleanupFailure(snapshot *wire.Snapshot) error {
	if snapshot.LastFailure != nil && snapshot.LastFailure.Code == string(sessionapi.FailureCleanup) {
		return failureError(snapshot.LastFailure)
	}
	return nil
}

func failureError(failure *wire.Failure) error {
	if failure == nil {
		return nil
	}
	return &controljson.CallError{Code: failure.Code, Message: failure.Message}
}

// configureStdin is the reader behind `dobby-cli configure -`.
var configureStdin io.Reader = os.Stdin

const (
	sourceSizeLimit = 1 << 20
	stdinSource     = "-"
	startCommand    = "start"
)

func readSource(source string) (data []byte, resultErr error) {
	if source == stdinSource {
		return readStdinSource(configureStdin)
	}
	cleanPath := filepath.Clean(source)
	if isInlineTOMLSource(source) {
		return readInlineTOMLSource(source, cleanPath)
	}

	sourceURL, isURL, err := parseSourceURL(source)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration URL: %w", err)
	}
	if isURL {
		return sourceURL, nil
	}
	return readSourceFile(cleanPath)
}

// readStdinSource reads a subscription from stdin so that it never appears in
// the process list or shell history. The text is an HTTPS URL (surrounding
// whitespace is ignored) or an inline TOML profile; a file path is not accepted.
// Errors never echo the input, which usually carries a secret token.
func readStdinSource(input io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(input, sourceSizeLimit+1))
	if err != nil {
		return nil, errors.New("cannot read the configuration from stdin")
	}
	if len(data) > sourceSizeLimit {
		return nil, errors.New("configuration exceeds the 1 MiB size limit")
	}
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("stdin is empty; pass an HTTPS subscription URL or TOML profile")
	}
	if isInlineTOMLSource(text) {
		return data, nil
	}
	trimmed := strings.TrimSpace(text)
	if _, isURL, parseErr := parseSourceURL(trimmed); parseErr != nil || !isURL {
		return nil, errors.New("stdin must contain an HTTPS subscription URL or a TOML profile")
	}
	return []byte(trimmed), nil
}

func readInlineTOMLSource(source, path string) ([]byte, error) {
	_, statErr := os.Stat(path)
	if statErr == nil {
		return readSourceFile(path)
	}
	if !errors.Is(statErr, os.ErrNotExist) &&
		!errors.Is(statErr, syscall.ENAMETOOLONG) &&
		!strings.ContainsAny(source, "\r\n") {
		return nil, fmt.Errorf("cannot read configuration file: %w", statErr)
	}
	if len(source) > sourceSizeLimit {
		return nil, errors.New("configuration exceeds the 1 MiB size limit")
	}
	return []byte(source), nil
}

func readSourceFile(path string) (data []byte, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read configuration file: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	data, err = io.ReadAll(io.LimitReader(file, sourceSizeLimit+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read configuration file: %w", err)
	}
	if len(data) > sourceSizeLimit {
		return nil, errors.New("configuration exceeds the 1 MiB size limit")
	}

	return data, nil
}

// Recognize an assignment only at the start of the first content line. The
// backend decodes the document and decides which keys and values are valid.
const tomlKeyPart = `(?:[A-Za-z0-9_-]+|'[^']*'|"(?:[^"\\]|\\.)*")`

var tomlAssignment = regexp.MustCompile(`^` + tomlKeyPart + `(?:[ \t]*\.[ \t]*` + tomlKeyPart + `)*[ \t]*=`)

func isInlineTOMLSource(source string) bool {
	var sawAssignment bool
	for line := range strings.SplitSeq(source, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate == "" || strings.HasPrefix(candidate, "#") {
			continue
		}
		if strings.HasPrefix(candidate, "[") {
			return true
		}
		if !sawAssignment {
			if !tomlAssignment.MatchString(candidate) {
				return false
			}
			// A single assignment can also be a filename. Look for the
			// following table before treating the source as an inline profile.
			sawAssignment = true
		}
	}
	return false
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
	value, err := publicip.Lookup(context.Background(), client, publicip.DefaultEndpoints)
	if err != nil {
		reportCLIError("external IP lookup failed", err)
		return exitRuntime
	}
	fmt.Println(value)
	return exitOK
}

func reportFailure(err error) int {
	if err == nil {
		err = errors.New("desktop backend returned no result")
	}
	var unavailable *serviceUnavailableError
	if errors.As(err, &unavailable) {
		fmt.Fprintf(os.Stderr, "dobby-cli: %v\n", unavailable)
		return exitConnect
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
	fmt.Println("dobby-cli configure <file-or-https-url|-> | start (--auto | --profile <index>) [--session-id <id> --config-digest <digest>] | stop [--session-id <id> --generation <number>] | snapshot | logs [--follow] | logs export <new-file> | logs upload | logs clear | external-ip")
	fmt.Println("configure -: read the HTTPS subscription URL or TOML profile from stdin (keeps the URL out of the process list).")
	fmt.Println("Exit codes: 0 success; 2 invalid command line or rejected configuration source; 3 Dobby VPN service unavailable; 4 operation failed; 8 conflict (state changed, take a new snapshot).")
}
