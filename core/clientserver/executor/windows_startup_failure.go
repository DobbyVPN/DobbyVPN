//go:build windows

package executor

import (
	"fmt"
	"os"
	"strings"

	"core/diagnostics"

	"golang.org/x/sys/windows/svc/eventlog"
)

// The MSI registers this source under the Application log. The registration
// points at EventCreate.exe, which only resolves event IDs 1-1000.
const (
	startupFailureEventSource = "DobbyVPN Go backend"
	startupFailureEventID     = 1
	startupFailureExitCode    = 1
	maxStartupFailureRunes    = 2000
)

// Replaced by tests; production writes to the Windows Event Log and exits.
var (
	writeStartupFailureEvent = writeEventLog
	exitProcess              = os.Exit
)

func writeEventLog(message string) error {
	logger, err := eventlog.Open(startupFailureEventSource)
	if err != nil {
		return err
	}
	err = logger.Error(startupFailureEventID, message)
	if closeErr := logger.Close(); err == nil {
		err = closeErr
	}
	return err
}

func startupFailureMessage(stage string, cause error) string {
	message := fmt.Sprintf("DobbyVPN Go backend failed to start (%s): %v", stage, cause)
	message = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, message)
	if runes := []rune(message); len(runes) > maxStartupFailureRunes {
		message = string(runes[:maxStartupFailureRunes]) + "..."
	}
	return message
}

// reportStartupFailure records a failure that happened before or outside the
// service control dispatcher and returns the process exit code. Failing to
// write the event never hides the cause: it is also printed to stderr.
func reportStartupFailure(stage string, cause error) int {
	message := startupFailureMessage(stage, cause)
	_, _ = fmt.Fprintln(diagnostics.Stderr, message)
	if err := writeStartupFailureEvent(message); err != nil {
		_, _ = fmt.Fprintf(diagnostics.Stderr, "failed to write startup failure to the Windows Event Log: %v\n", err)
	}
	return startupFailureExitCode
}
