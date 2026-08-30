package v2

import (
	"sync/atomic"
	"time"
)

const (
	defaultAuditBuffer        = 256
	defaultAuditFlushTimeout  = 2 * time.Second
	auditEventOperation       = "operation"
	AuditEventStateTransition = "state.transition"
	AuditEventStatusSnapshot  = "status.snapshot"
	auditPhaseBegin           = "begin"
	auditPhaseEnd             = "end"
)

// AuditEvent is a safe, configuration-free diagnostic fact. It deliberately
// contains only the lifecycle vocabulary already exposed by sessionapi/v2.
// Sinks must never add a raw session ID, command ID, configuration, endpoint,
// observed public IP, or unrestricted error text.
type AuditEvent struct {
	OccurredAt      time.Time
	Event           string
	Operation       string
	Phase           string
	Outcome         string
	DurationMillis  int64
	Generation      uint64
	Sequence        uint64
	PreviousState   State
	State           State
	Configured      bool
	CleanupComplete bool
	HasProfile      bool
	ProfileIndex    int
	Protocol        Protocol
	Failure         FailureCode
	WarningCode     string
	DroppedBefore   uint64
}

// AuditSink receives ordered, already-sanitized lifecycle facts. Sinks must
// return promptly and must not call back into the manager.
type AuditSink interface {
	RecordAudit(AuditEvent)
}

type AuditSinkFunc func(AuditEvent)

func (f AuditSinkFunc) RecordAudit(event AuditEvent) { f(event) }

type auditItem struct {
	event   AuditEvent
	barrier chan struct{}
}

type auditRecorder struct {
	sink    AuditSink
	queue   chan auditItem
	dropped atomic.Uint64
}

type auditOperation struct {
	recorder  *auditRecorder
	operation string
	started   time.Time
}

func newAuditRecorder(sink AuditSink) *auditRecorder {
	if sink == nil {
		return nil
	}
	recorder := &auditRecorder{sink: sink, queue: make(chan auditItem, defaultAuditBuffer)}
	go func() {
		for item := range recorder.queue {
			if item.barrier != nil {
				close(item.barrier)
				continue
			}
			recorder.sink.RecordAudit(item.event)
		}
	}()
	return recorder
}

func (r *auditRecorder) prepare(event AuditEvent) AuditEvent {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	event.DroppedBefore = r.dropped.Swap(0)
	return event
}

func (r *auditRecorder) record(event AuditEvent) {
	if r == nil {
		return
	}
	event = r.prepare(event)
	select {
	case r.queue <- auditItem{event: event}:
	default:
		r.dropped.Add(event.DroppedBefore + 1)
	}
}

func (r *auditRecorder) recordBounded(event AuditEvent, timeout time.Duration) bool {
	if r == nil {
		return true
	}
	event = r.prepare(event)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r.queue <- auditItem{event: event}:
		return true
	case <-timer.C:
		r.dropped.Add(event.DroppedBefore + 1)
		return false
	}
}

// flush waits only at an orderly terminal boundary. Ordinary lifecycle calls
// remain isolated from local disk latency; the timeout prevents shutdown from
// hanging forever if a sink is wedged.
func (r *auditRecorder) flush(timeout time.Duration) bool {
	if r == nil {
		return true
	}
	barrier := make(chan struct{})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r.queue <- auditItem{barrier: barrier}:
	case <-timer.C:
		return false
	}
	select {
	case <-barrier:
		return true
	case <-timer.C:
		return false
	}
}

func (r *auditRecorder) begin(operation string) auditOperation {
	if r == nil {
		return auditOperation{}
	}
	started := time.Now()
	r.record(AuditEvent{OccurredAt: started, Event: auditEventOperation, Operation: operation, Phase: auditPhaseBegin})
	return auditOperation{recorder: r, operation: operation, started: started}
}

func (operation auditOperation) result(err error) AuditEvent {
	outcome := "success"
	failureCode := FailureCode("")
	if err != nil {
		outcome = "failure"
		failureCode = CodeOf(err)
	}
	return AuditEvent{
		OccurredAt:     time.Now(),
		Event:          auditEventOperation,
		Operation:      operation.operation,
		Phase:          auditPhaseEnd,
		Outcome:        outcome,
		DurationMillis: time.Since(operation.started).Milliseconds(),
		Failure:        failureCode,
	}
}

func (operation auditOperation) end(err error) {
	if operation.recorder != nil {
		operation.recorder.record(operation.result(err))
	}
}

func (operation auditOperation) endAndFlush(err error, timeout time.Duration) {
	if operation.recorder == nil {
		return
	}
	operation.recorder.recordBounded(operation.result(err), timeout)
	operation.recorder.flush(timeout)
}

func (r *auditRecorder) snapshot(snapshot SnapshotResult, reason string) {
	if r == nil {
		return
	}
	event := AuditEvent{
		Event:           AuditEventStatusSnapshot,
		Operation:       reason,
		Generation:      snapshot.Generation,
		State:           snapshot.State,
		Configured:      snapshot.Configured,
		CleanupComplete: snapshot.CleanupComplete,
		Failure:         snapshot.LastFailure,
	}
	if snapshot.ActiveProfile != nil {
		event.HasProfile = true
		event.ProfileIndex = snapshot.ActiveProfile.Index
		event.Protocol = snapshot.ActiveProfile.Protocol
	}
	r.record(event)
}
