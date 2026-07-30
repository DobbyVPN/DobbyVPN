package v1

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAuditPreservesActionTransitionAndStatusContext(t *testing.T) {
	events := make(chan AuditEvent, 32)
	manager := NewManager(ManagerOptions{Audit: AuditSinkFunc(func(event AuditEvent) {
		events <- event
	})})

	sessionID, err := manager.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	requireConfiguredSnapshot(t, manager, sessionID)
	recorded := receiveAuditEvents(t, events, 10)

	assertOperationPair(t, recorded, "create_session")
	assertOperationPair(t, recorded, "configure")
	assertOperationPair(t, recorded, "snapshot")

	transition, status := configuredAuditContext(recorded)
	if transition == nil {
		t.Fatalf("configured transition missing: %#v", recorded)
	}
	if transition.PreviousState != StateIdle || !transition.Configured || !transition.CleanupComplete {
		t.Fatalf("transition lost before/after status: %#v", *transition)
	}
	if status == nil || status.State != StateConfigured || !status.Configured || !status.CleanupComplete {
		t.Fatalf("interim status snapshot missing: %#v", recorded)
	}
}

func requireConfiguredSnapshot(t *testing.T, manager *Manager, sessionID string) {
	t.Helper()
	if _, configureErr := manager.Configure(context.Background(), sessionID, "configure", fixture(t)); configureErr != nil {
		t.Fatal(configureErr)
	}
	snapshot, snapshotErr := manager.Snapshot(context.Background(), sessionID)
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if snapshot.State != StateConfigured || !snapshot.Configured || !snapshot.CleanupComplete {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func receiveAuditEvents(t *testing.T, events <-chan AuditEvent, count int) []AuditEvent {
	t.Helper()
	recorded := make([]AuditEvent, 0, count)
	deadline := time.After(2 * time.Second)
	for len(recorded) < count {
		select {
		case event := <-events:
			recorded = append(recorded, event)
		case <-deadline:
			t.Fatalf("timed out after %d audit events: %#v", len(recorded), recorded)
		}
	}
	return recorded
}

func configuredAuditContext(events []AuditEvent) (transition, status *AuditEvent) {
	for index := range events {
		event := &events[index]
		if event.Event == AuditEventStateTransition && event.State == StateConfigured {
			transition = event
		}
		if event.Event == AuditEventStatusSnapshot && event.Operation == "snapshot" {
			status = event
		}
	}
	return transition, status
}

func TestDestroyFlushesTerminalAuditEvents(t *testing.T) {
	var mu sync.Mutex
	var recorded []AuditEvent
	manager := NewManager(ManagerOptions{Audit: AuditSinkFunc(func(event AuditEvent) {
		time.Sleep(time.Millisecond)
		mu.Lock()
		recorded = append(recorded, event)
		mu.Unlock()
	})})

	sessionID, err := manager.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.DestroySession(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	var destroyedTransition, destroyEnd bool
	for _, event := range recorded {
		if event.OccurredAt.IsZero() {
			t.Fatalf("audit event lost occurrence time: %#v", event)
		}
		if event.Event == AuditEventStateTransition && event.State == StateDestroyed {
			destroyedTransition = true
		}
		if event.Event == auditEventOperation && event.Operation == "destroy_session" && event.Phase == auditPhaseEnd {
			destroyEnd = true
		}
	}
	if !destroyedTransition || !destroyEnd {
		t.Fatalf("DestroySession returned before terminal audit delivery: %#v", recorded)
	}
}

func assertOperationPair(t *testing.T, events []AuditEvent, operation string) {
	t.Helper()
	begin := -1
	end := -1
	for index, event := range events {
		if event.Event != auditEventOperation || event.Operation != operation {
			continue
		}
		switch event.Phase {
		case auditPhaseBegin:
			begin = index
		case auditPhaseEnd:
			end = index
			if event.Outcome != "success" || event.Failure != "" || event.DurationMillis < 0 {
				t.Fatalf("invalid %s result: %#v", operation, event)
			}
		}
	}
	if begin < 0 || end <= begin {
		t.Fatalf("%s begin/end ordering missing: %#v", operation, events)
	}
}
