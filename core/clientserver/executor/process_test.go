package executor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"core/sessionapi"
	"core/sessionapi/mobilebinding"
	"core/sessionapi/wire"
)

type shutdownTestRuntime struct {
	starts      chan sessionapi.RuntimeProfile
	stopEntered chan struct{}
	releaseStop chan struct{}
	stopOnce    sync.Once
}

func (r *shutdownTestRuntime) Start(_ context.Context, _ sessionapi.SessionRef, profile sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	r.starts <- profile
	return shutdownTestLease{runtime: r}, nil
}

type shutdownTestLease struct{ runtime *shutdownTestRuntime }

func (l shutdownTestLease) Stop(context.Context) error {
	l.runtime.stopOnce.Do(func() { close(l.runtime.stopEntered) })
	<-l.runtime.releaseStop
	return nil
}

func executorSnapshot(t *testing.T, binding *mobilebinding.Binding, sessionID string) wire.Snapshot {
	t.Helper()
	var response wire.Response[wire.Snapshot]
	if err := json.Unmarshal([]byte(binding.Snapshot(sessionID)), &response); err != nil {
		t.Fatalf("decode process Snapshot: %v", err)
	}
	if !response.OK {
		t.Fatalf("process Snapshot failed: %#v", response.Error)
	}
	return response.Result
}

func waitExecutorState(t *testing.T, binding *mobilebinding.Binding, sessionID, state string) wire.Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := executorSnapshot(t, binding, sessionID)
		if snapshot.State == state {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot := executorSnapshot(t, binding, sessionID)
	t.Fatalf("process Snapshot did not reach %s: %#v", state, snapshot)
	return wire.Snapshot{}
}

func TestDesktopShutdownOwnerCancelsPendingSwitch(t *testing.T) {
	const config = `[[Outline]]
Description = "active profile"
Server = "active.example.invalid"
Port = 443
Password = "active-synthetic-password"

[[Outline]]
Description = "pending profile"
Server = "pending.example.invalid"
Port = 443
Password = "pending-synthetic-password"
`
	runtime := &shutdownTestRuntime{
		starts:      make(chan sessionapi.RuntimeProfile, 4),
		stopEntered: make(chan struct{}),
		releaseStop: make(chan struct{}),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runtime.releaseStop) }) }
	binding := mobilebinding.NewForDesktop(sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: runtime}))
	t.Cleanup(func() {
		release()
		if err := binding.StopAndWait(context.Background()); err != nil {
			t.Errorf("shutdown test cleanup: %v", err)
		}
	})

	initial := executorSnapshot(t, binding, "")
	configured := binding.Configure(initial.SessionID, int64(initial.Sequence), []byte(config))
	var configuration wire.Response[wire.Configuration]
	if err := json.Unmarshal([]byte(configured), &configuration); err != nil || !configuration.OK {
		t.Fatalf("configure test profiles: response=%s error=%v", configured, err)
	}
	started := binding.StartSelection(initial.SessionID, int64(configuration.Result.Sequence), string(sessionapi.ProfileIndex), 0, configuration.Result.Digest, false)
	var start wire.Response[wire.Generation]
	if err := json.Unmarshal([]byte(started), &start); err != nil || !start.OK {
		t.Fatalf("start active profile: response=%s error=%v", started, err)
	}
	connected := waitExecutorState(t, binding, initial.SessionID, string(sessionapi.StateConnected))
	if profile := <-runtime.starts; profile.Summary.Description != "active profile" {
		t.Fatalf("active profile start = %#v", profile.Summary)
	}

	switching := binding.StartSelection(initial.SessionID, int64(connected.Sequence), string(sessionapi.ProfileIndex), 1, configuration.Result.Digest, true)
	var switchStart wire.Response[wire.Generation]
	if err := json.Unmarshal([]byte(switching), &switchStart); err != nil || !switchStart.OK {
		t.Fatalf("request pending profile switch: response=%s error=%v", switching, err)
	}
	select {
	case <-runtime.stopEntered:
	case <-time.After(time.Second):
		t.Fatal("pending switch did not begin active lease cleanup")
	}
	pending := executorSnapshot(t, binding, initial.SessionID)
	if pending.State != string(sessionapi.StateStopping) || pending.PendingTarget == nil || pending.PendingTarget.Index != 1 {
		t.Fatalf("pending switch Snapshot = %#v", pending)
	}

	controlClosed := make(chan struct{}, 1)
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownDesktop(func() error {
			controlClosed <- struct{}{}
			return nil
		}, nil, binding)
	}()
	select {
	case <-controlClosed:
	case <-time.After(time.Second):
		t.Fatal("desktop shutdown callback did not close its control endpoint")
	}
	deadline := time.Now().Add(time.Second)
	for {
		current := executorSnapshot(t, binding, initial.SessionID)
		if current.PendingTarget == nil {
			if current.State != string(sessionapi.StateStopping) || current.PrimaryAction != "NONE" {
				t.Fatalf("desktop shutdown callback exposed an invalid stopping Snapshot: %#v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("desktop shutdown callback did not cancel pending switch: %#v", current)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("desktop shutdown callback returned before lease cleanup: %v", err)
	default:
	}
	release()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("desktop shutdown callback failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("desktop shutdown callback did not finish after lease cleanup")
	}
	idle := waitExecutorState(t, binding, initial.SessionID, string(sessionapi.StateIdle))
	if idle.PendingTarget != nil || idle.Generation != connected.Generation {
		t.Fatalf("desktop shutdown restarted the pending profile: %#v", idle)
	}
	select {
	case profile := <-runtime.starts:
		t.Fatalf("pending profile started after desktop shutdown: %#v", profile.Summary)
	default:
	}
}
