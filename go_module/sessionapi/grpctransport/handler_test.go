package grpctransport

import (
	"context"
	"errors"
	"testing"
	"time"

	"go_module/grpcproto"
	"go_module/sessionapi"
)

const testConfig = "[[Outline]]\nServer=\"vpn.invalid\"\nPort=443\nPassword=\"secret\"\n"

func TestHandlerValidatesWithoutMutationAndUsesSnapshotRevisions(t *testing.T) {
	h := New(sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: testRuntime{}, Platform: testPlatform{}}))
	ctx := context.Background()
	state := handlerInitialSnapshot(ctx, t, h)
	assertHandlerValidationIsStateless(ctx, t, h, state)
	configured := handlerConfigure(ctx, t, h, state)
	started := handlerStart(ctx, t, h, state, configured)
	assertHandlerStartRejectsStaleRevision(ctx, t, h, state, configured)
	waitHandlerConnected(ctx, t, h, state)
	assertHandlerStopRejectsStaleGeneration(ctx, t, h, state, started)
	stopHandler(ctx, t, h, state, started)
}

func handlerInitialSnapshot(ctx context.Context, t *testing.T, h *Handler) *grpcproto.SessionSnapshot {
	t.Helper()
	initial, err := h.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{})
	if err != nil || initial.GetFailure() != nil {
		t.Fatalf("initial Snapshot = %#v, %v", initial, err)
	}
	state := initial.GetSnapshot()
	if state.GetSessionId() == "" || state.GetSequence() == 0 {
		t.Fatalf("Snapshot did not identify owner and revision: %#v", state)
	}
	return state
}

func assertHandlerValidationIsStateless(ctx context.Context, t *testing.T, h *Handler, state *grpcproto.SessionSnapshot) {
	t.Helper()
	validated, err := h.ValidateConfig(ctx, &grpcproto.SessionValidateConfigRequest{RawConfig: []byte(testConfig)})
	if err != nil || validated.GetFailure() != nil || validated.GetDigest() == "" || validated.GetSourceKind() != grpcproto.SessionSourceKind_SESSION_SOURCE_KIND_INLINE {
		t.Fatalf("ValidateConfig = %#v, %v", validated, err)
	}
	unchanged, err := h.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: state.GetSessionId()})
	if err != nil || unchanged.GetSnapshot().GetSequence() != state.GetSequence() || unchanged.GetSnapshot().GetConfigured() {
		t.Fatalf("validation mutated session: %#v, %v", unchanged, err)
	}
}

func handlerConfigure(ctx context.Context, t *testing.T, h *Handler, state *grpcproto.SessionSnapshot) *grpcproto.SessionConfigureResponse {
	t.Helper()
	configured, err := h.Configure(ctx, &grpcproto.SessionConfigureRequest{
		SessionId: state.GetSessionId(), ExpectedSequence: state.GetSequence(), RawConfig: []byte(testConfig),
	})
	if err != nil || configured.GetFailure() != nil || configured.GetSequence() <= state.GetSequence() {
		t.Fatalf("Configure = %#v, %v", configured, err)
	}
	return configured
}

func handlerStart(ctx context.Context, t *testing.T, h *Handler, state *grpcproto.SessionSnapshot, configured *grpcproto.SessionConfigureResponse) *grpcproto.SessionStartResponse {
	t.Helper()
	started, err := h.Start(ctx, &grpcproto.SessionStartRequest{
		SessionId: state.GetSessionId(), ExpectedSequence: configured.GetSequence(),
		Mode: grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX,
	})
	if err != nil || started.GetFailure() != nil || started.GetGeneration() == 0 {
		t.Fatalf("Start = %#v, %v", started, err)
	}
	return started
}

func assertHandlerStartRejectsStaleRevision(ctx context.Context, t *testing.T, h *Handler, state *grpcproto.SessionSnapshot, configured *grpcproto.SessionConfigureResponse) {
	t.Helper()
	staleStart, err := h.Start(ctx, &grpcproto.SessionStartRequest{
		SessionId: state.GetSessionId(), ExpectedSequence: configured.GetSequence(),
		Mode: grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX,
	})
	if err != nil || staleStart.GetFailure().GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_CONFLICT {
		t.Fatalf("stale Start = %#v, %v", staleStart, err)
	}
}

func waitHandlerConnected(ctx context.Context, t *testing.T, h *Handler, state *grpcproto.SessionSnapshot) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, snapshotErr := h.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: state.GetSessionId()})
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if snapshot.GetSnapshot().GetState() == grpcproto.SessionState_SESSION_STATE_CONNECTED {
			break
		}
		time.Sleep(time.Millisecond)
	}
}

func assertHandlerStopRejectsStaleGeneration(ctx context.Context, t *testing.T, h *Handler, state *grpcproto.SessionSnapshot, started *grpcproto.SessionStartResponse) {
	t.Helper()
	staleStop, err := h.Stop(ctx, &grpcproto.SessionStopRequest{SessionId: state.GetSessionId(), Generation: started.GetGeneration() + 1})
	if err != nil || staleStop.GetFailure().GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_STALE_GENERATION {
		t.Fatalf("stale Stop = %#v, %v", staleStop, err)
	}
}

func stopHandler(ctx context.Context, t *testing.T, h *Handler, state *grpcproto.SessionSnapshot, started *grpcproto.SessionStartResponse) {
	t.Helper()
	if stopped, stopErr := h.Stop(ctx, &grpcproto.SessionStopRequest{SessionId: state.GetSessionId(), Generation: started.GetGeneration()}); stopErr != nil || stopped.GetFailure() != nil {
		t.Fatalf("Stop = %#v, %v", stopped, stopErr)
	}
}

func TestHandlerMapsDomainFailures(t *testing.T) {
	h := New(sessionapi.NewManager(sessionapi.ManagerOptions{}))
	response, err := h.Start(context.Background(), &grpcproto.SessionStartRequest{Mode: grpcproto.SessionStartMode_SESSION_START_MODE_UNSPECIFIED})
	if err != nil || response.GetFailure().GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_INVALID_ARGUMENT {
		t.Fatalf("failure mapping = %#v, %v", response, err)
	}
}

func TestHandlerPreservesAsyncFailureMessageInSnapshot(t *testing.T) {
	const exact = "runtime start failed: dial tcp: i/o timeout"
	h := New(sessionapi.NewManager(sessionapi.ManagerOptions{
		Runtime:  &asyncFailureRuntime{err: errors.New(exact)},
		Platform: testPlatform{},
	}))
	ctx := context.Background()
	initial, err := h.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := h.Configure(ctx, &grpcproto.SessionConfigureRequest{
		SessionId: initial.GetSnapshot().GetSessionId(), ExpectedSequence: initial.GetSnapshot().GetSequence(), RawConfig: []byte(testConfig),
	})
	if err != nil || configured.GetFailure() != nil {
		t.Fatalf("Configure = %#v, %v", configured, err)
	}
	started, err := h.Start(ctx, &grpcproto.SessionStartRequest{
		SessionId: initial.GetSnapshot().GetSessionId(), ExpectedSequence: configured.GetSequence(),
		Mode: grpcproto.SessionStartMode_SESSION_START_MODE_PROFILE_INDEX,
	})
	if err != nil || started.GetFailure() != nil {
		t.Fatalf("Start = %#v, %v", started, err)
	}
	want := "RUNTIME_FAILED: operation failed: " + exact
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, snapshotErr := h.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: initial.GetSnapshot().GetSessionId()})
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		failure := snapshot.GetSnapshot().GetLastFailure()
		if failure == nil {
			time.Sleep(time.Millisecond)
			continue
		}
		if failure.GetCode() != grpcproto.SessionFailureCode_SESSION_FAILURE_CODE_RUNTIME_FAILED || failure.GetMessage() != want {
			t.Fatalf("snapshot failure = %#v, want message %q", failure, want)
		}
		return
	}
	t.Fatal("async runtime failure did not reach the snapshot")
}

type testRuntime struct{}

type asyncFailureRuntime struct{ err error }

func (r *asyncFailureRuntime) Probe(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile) (sessionapi.ProbeResult, error) {
	return sessionapi.ProbeResult{LatencyMillis: 1}, nil
}
func (r *asyncFailureRuntime) Start(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	return nil, r.err
}

func (testRuntime) Probe(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile) (sessionapi.ProbeResult, error) {
	return sessionapi.ProbeResult{LatencyMillis: 1}, nil
}
func (testRuntime) Start(context.Context, sessionapi.SessionRef, sessionapi.RuntimeProfile) (sessionapi.RuntimeLease, error) {
	return testLease{}, nil
}

type testLease struct{}

func (testLease) Stop(context.Context) error { return nil }

type testPlatform struct{}

func (testPlatform) PrepareTunnel(context.Context, sessionapi.SessionRef) (sessionapi.PlatformLease, error) {
	return testPlatformLease{}, nil
}
func (testPlatform) ProtectSocket(context.Context, sessionapi.SessionRef, int) error { return nil }
func (testPlatform) PublishState(context.Context, sessionapi.StateChange)            {}

type testPlatformLease struct{}

func (testPlatformLease) Release(context.Context) error { return nil }
