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
	if _, err := manager.Configure(context.Background(), sessionID, "configure", fixture(t)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != StateConfigured || !snapshot.Configured || !snapshot.CleanupComplete {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}

	var recorded []AuditEvent
	deadline := time.After(2 * time.Second)
	for len(recorded) < 10 {
		select {
		case event := <-events:
			recorded = append(recorded, event)
		case <-deadline:
			t.Fatalf("timed out after %d audit events: %#v", len(recorded), recorded)
		}
	}

	assertOperationPair(t, recorded, "create_session")
	assertOperationPair(t, recorded, "configure")
	assertOperationPair(t, recorded, "snapshot")

	var transition *AuditEvent
	var status *AuditEvent
	for index := range recorded {
		event := &recorded[index]
		if event.Event == "state.transition" && event.State == StateConfigured {
			transition = event
		}
		if event.Event == "status.snapshot" && event.Operation == "snapshot" {
			status = event
		}
	}
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
		if event.Event == "state.transition" && event.State == StateDestroyed {
			destroyedTransition = true
		}
		if event.Event == "operation" && event.Operation == "destroy_session" && event.Phase == "end" {
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
		if event.Event != "operation" || event.Operation != operation {
			continue
		}
		switch event.Phase {
		case "begin":
			begin = index
		case "end":
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
