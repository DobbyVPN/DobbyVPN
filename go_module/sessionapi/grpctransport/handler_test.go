package grpctransport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"go_module/grpcproto"
	v1 "go_module/sessionapi/v2"
)

func TestHandlerExactRawBytesOrderedObserveStaleAndIdempotentStart(t *testing.T) {
	h := New(v1.NewManager(v1.ManagerOptions{Runtime: testRuntime{}, Platform: testPlatform{}}))
	ctx := context.Background()
	created, err := h.CreateSession(ctx, &grpcproto.SessionCreateSessionRequest{})
	if err != nil || created.GetFailure() != nil {
		t.Fatalf("CreateSession = %#v, %v", created, err)
	}
	raw := []byte("\n[[Outline]]\nServer = \"vpn.invalid\"\nPort = 443\nPassword = \"secret\"\n")
	configured, err := h.Configure(ctx, &grpcproto.SessionConfigureRequest{SessionId: created.GetSessionId(), CommandId: "configure", RawConfig: raw})
	if err != nil || configured.GetFailure() != nil {
		t.Fatalf("Configure = %#v, %v", configured, err)
	}
	digest := sha256.Sum256(raw)
	if configured.GetDigest() != hex.EncodeToString(digest[:]) {
		t.Fatalf("raw bytes changed: %q", configured.GetDigest())
	}
	first, err := h.Start(ctx, &grpcproto.SessionStartRequest{SessionId: created.GetSessionId(), CommandId: "start", Mode: grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX})
	if err != nil || first.GetFailure() != nil {
		t.Fatalf("Start = %#v, %v", first, err)
	}
	second, err := h.Start(ctx, &grpcproto.SessionStartRequest{SessionId: created.GetSessionId(), CommandId: "start", Mode: grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX})
	if err != nil || second.GetFailure() != nil || second.GetGeneration() != first.GetGeneration() {
		t.Fatalf("duplicate start = %#v, %v", second, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := h.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: created.GetSessionId()})
		if snapshot.GetSnapshot().GetState() == grpcproto.SessionState_SESSION_STATE_CONNECTED {
			break
		}
		time.Sleep(time.Millisecond)
	}
	stale, err := h.Stop(ctx, &grpcproto.SessionStopRequest{SessionId: created.GetSessionId(), CommandId: "stale", Generation: first.GetGeneration() + 1})
	if err != nil || stale.GetFailure().GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION {
		t.Fatalf("stale stop = %#v, %v", stale, err)
	}
	observed, err := h.Observe(ctx, &grpcproto.SessionObserveRequest{SessionId: created.GetSessionId()})
	if err != nil || observed.GetFailure() != nil {
		t.Fatalf("Observe = %#v, %v", observed, err)
	}
	for index, event := range observed.GetEvents() {
		if event.GetSequence() != uint64(index+1) {
			t.Fatalf("event order %d: %#v", index, event)
		}
	}
}

func TestHandlerMapsDomainFailures(t *testing.T) {
	h := New(v1.NewManager(v1.ManagerOptions{}))
	response, err := h.Start(context.Background(), &grpcproto.SessionStartRequest{Mode: grpcproto.SessionStartMode_SESSION_START_MODE_UNSPECIFIED})
	if err != nil || response.GetFailure().GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT {
		t.Fatalf("failure mapping = %#v, %v", response, err)
	}
}

func TestHandlerPreservesAsyncFailureMessageInEventAndSnapshot(t *testing.T) {
	const exact = "runtime start failed: dial tcp: i/o timeout"
	h := New(v1.NewManager(v1.ManagerOptions{
		Runtime:  &asyncFailureRuntime{err: errors.New(exact)},
		Platform: testPlatform{},
	}))
	ctx := context.Background()
	created, err := h.CreateSession(ctx, &grpcproto.SessionCreateSessionRequest{})
	if err != nil || created.GetFailure() != nil {
		t.Fatalf("CreateSession = %#v, %v", created, err)
	}
	configured, err := h.Configure(ctx, &grpcproto.SessionConfigureRequest{
		SessionId: created.GetSessionId(),
		CommandId: "configure",
		RawConfig: []byte("[[Outline]]\nServer=\"vpn.invalid\"\nPort=443\nPassword=\"secret\"\n"),
	})
	if err != nil || configured.GetFailure() != nil {
		t.Fatalf("Configure = %#v, %v", configured, err)
	}
	started, err := h.Start(ctx, &grpcproto.SessionStartRequest{
		SessionId: created.GetSessionId(),
		CommandId: "start",
		Mode:      grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX,
	})
	if err != nil || started.GetFailure() != nil {
		t.Fatalf("Start = %#v, %v", started, err)
	}
	want := "RUNTIME_FAILED: operation failed: " + exact
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, snapshotErr := h.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: created.GetSessionId()})
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if failure := snapshot.GetSnapshot().GetLastFailure(); failure != nil {
			if failure.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED || failure.GetMessage() != want {
				t.Fatalf("snapshot failure = %#v, want message %q", failure, want)
			}
			observed, observeErr := h.Observe(ctx, &grpcproto.SessionObserveRequest{SessionId: created.GetSessionId()})
			if observeErr != nil {
				t.Fatal(observeErr)
			}
			for _, event := range observed.GetEvents() {
				if failure := event.GetFailure(); failure != nil && failure.GetCode() == grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED {
					if failure.GetMessage() != want {
						t.Fatalf("event failure = %#v, want message %q", failure, want)
					}
					return
				}
			}
			t.Fatal("snapshot failure had no corresponding runtime failure event")
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("async runtime failure did not reach the snapshot")
}

type testRuntime struct{}

type asyncFailureRuntime struct{ err error }

func (r *asyncFailureRuntime) Probe(context.Context, v1.SessionRef, v1.RuntimeProfile) (v1.ProbeResult, error) {
	return v1.ProbeResult{LatencyMillis: 1}, nil
}
func (r *asyncFailureRuntime) Start(context.Context, v1.SessionRef, v1.RuntimeProfile) (v1.RuntimeLease, error) {
	return nil, r.err
}

func (testRuntime) Probe(context.Context, v1.SessionRef, v1.RuntimeProfile) (v1.ProbeResult, error) {
	return v1.ProbeResult{LatencyMillis: 1}, nil
}
func (testRuntime) Start(context.Context, v1.SessionRef, v1.RuntimeProfile) (v1.RuntimeLease, error) {
	return testLease{}, nil
}

type testLease struct{}

func (testLease) Stop(context.Context) error { return nil }

type testPlatform struct{}

func (testPlatform) PrepareTunnel(context.Context, v1.SessionRef) (v1.PlatformLease, error) {
	return testPlatformLease{}, nil
}
func (testPlatform) ProtectSocket(context.Context, v1.SessionRef, int) error { return nil }
func (testPlatform) PublishState(context.Context, v1.Event)                  {}

type testPlatformLease struct{}

func (testPlatformLease) Release(context.Context) error { return nil }
