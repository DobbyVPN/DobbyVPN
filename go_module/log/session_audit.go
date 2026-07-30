package log

import (
	"fmt"
	"log/slog"

	v1 "go_module/sessionapi/v1"
)

// SessionAuditSink renders sessionapi/v1's configuration-free diagnostic
// facts through the same redacting JSONL writer as the existing Go logs.
type SessionAuditSink struct{}

func (SessionAuditSink) RecordAudit(event v1.AuditEvent) {
	level := slog.LevelDebug
	eventName := event.Event
	message := "session status recorded"
	fields := make(map[string]any)

	switch event.Event {
	case "operation":
		eventName += "." + event.Phase
		fields["operation"] = event.Operation
		fields["phase"] = event.Phase
		if event.Phase == "begin" {
			level = slog.LevelDebug - 4
			message = event.Operation + " started"
		} else {
			fields["outcome"] = event.Outcome
			fields["duration_ms"] = event.DurationMillis
			message = fmt.Sprintf("%s completed outcome=%s durationMs=%d", event.Operation, event.Outcome, event.DurationMillis)
			if event.Failure != "" {
				level = slog.LevelWarn
				fields["failure_code"] = event.Failure
			}
		}
	case v1.AuditEventStateTransition:
		level = slog.LevelInfo
		message = fmt.Sprintf("session state changed %s -> %s", event.PreviousState, event.State)
		fields["state_before"] = event.PreviousState
		fields["state_after"] = event.State
		if event.State == v1.StateFailed {
			level = slog.LevelError
		}
	case v1.AuditEventStatusSnapshot:
		message = fmt.Sprintf(
			"session status state=%s generation=%d configured=%t cleanupComplete=%t",
			event.State, event.Generation, event.Configured, event.CleanupComplete,
		)
		fields["state"] = event.State
	}

	if event.Generation != 0 {
		fields["generation"] = event.Generation
	}
	if event.Sequence != 0 {
		fields["sequence"] = event.Sequence
	}
	if event.Event == v1.AuditEventStateTransition || event.Event == v1.AuditEventStatusSnapshot {
		fields["configured"] = event.Configured
		fields["cleanup_complete"] = event.CleanupComplete
	}
	if event.HasProfile {
		fields["profile_index"] = event.ProfileIndex
		fields["protocol"] = event.Protocol
	}
	if event.Failure != "" {
		fields["failure_code"] = event.Failure
	}
	if event.WarningCode != "" {
		fields["warning_code"] = event.WarningCode
	}
	if event.DroppedBefore != 0 {
		level = slog.LevelWarn
		fields["dropped_before"] = event.DroppedBefore
	}

	writeEventAt(event.OccurredAt, level, eventName, "SESSION", message, fields)
}
