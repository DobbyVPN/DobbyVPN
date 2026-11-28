package runtime

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"core/dnscache"
	"core/protocol"
	"core/sessionapi"
	"core/tunnel"
	"core/xray"
)

const xrayOnlyConfig = `
[[Xray]]
Description = "xray"
outbounds = [{ tag = "proxy", protocol = "vless", settings = { vnext = [] } }]
`

// protectionCore stands for the platform network core: Connect turns the
// fail-closed protection on and Disconnect releases it.
type protectionCore struct{ on atomic.Bool }

func (c *protectionCore) Connect(context.Context) error {
	c.on.Store(true)
	return nil
}
func (c *protectionCore) Disconnect(context.Context) error {
	c.on.Store(false)
	return nil
}
func (*protectionCore) Attach(context.Context, uint64, string, *dnscache.Cache, *tunnel.BypassPolicy) error {
	return nil
}
func (*protectionCore) Detach(context.Context, uint64) error { return nil }
func (*protectionCore) ProtocolParameters() (int, string)    { return 0, "" }

func waitSnapshot(t *testing.T, m *sessionapi.Manager, id string, done func(sessionapi.SnapshotResult) bool) sessionapi.SnapshotResult {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshot, err := m.Snapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if done(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("session did not settle: %#v", snapshot)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The protocol device fails to start -> Stop releases the protection and the
// internet works.
func TestProtocolDeviceFailsToStartThenStopReleasesProtection(t *testing.T) {
	core := &protectionCore{}
	r := New(Options{
		// The real Xray constructor fails and its nil device reaches the
		// runtime through the protocol interface, as in runtimebridge. On the
		// reported laptop the cause was a refused local port; here the cache
		// is missing, which takes the same "return nil, err" path.
		NewDevice: func(ctx context.Context, profile sessionapi.RuntimeProfile, _ *dnscache.Cache) (protocol.ProtocolDevice, error) {
			return xray.NewXrayDevice(ctx, profile.Config.Xray, nil)
		},
		NewCore: func(io.ReadWriteCloser, *dnscache.Cache) sessionCore { return core },
	})
	m := sessionapi.NewManager(sessionapi.ManagerOptions{Runtime: r})
	snapshot, err := m.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	id := snapshot.SessionID
	if _, err = m.Configure(context.Background(), id, snapshot.Sequence, []byte(xrayOnlyConfig)); err != nil {
		t.Fatal(err)
	}
	snapshot, err = m.Snapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	started, err := m.Start(context.Background(), id, snapshot.Sequence, sessionapi.StartTarget{Mode: sessionapi.ProfileIndex, Index: 0})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitSnapshot(t, m, id, func(s sessionapi.SnapshotResult) bool { return s.State == sessionapi.StateFailed })
	t.Logf("after the failed start: failure=%s message=%q vpn_active=%t", failed.LastFailure, failed.LastFailureMessage, failed.VPNActive)

	if _, err = m.Stop(context.Background(), id, started.Generation); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	stopped := waitSnapshot(t, m, id, func(s sessionapi.SnapshotResult) bool {
		return s.State != sessionapi.StateStopping && (s.CleanupComplete || s.State == sessionapi.StateFailed)
	})
	if stopped.State == sessionapi.StateFailed || !stopped.CleanupComplete {
		t.Fatalf("Stop did not finish: state=%s failure=%s message=%q", stopped.State, stopped.LastFailure, stopped.LastFailureMessage)
	}
	if stopped.VPNActive {
		t.Fatal("vpn_active stayed true after Stop")
	}
	if core.on.Load() {
		t.Fatal("the fail-closed protection is still on after Stop: the internet stays blocked")
	}
}
