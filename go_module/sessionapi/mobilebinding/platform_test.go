package mobilebinding

import (
	"context"
	"math"
	"os"
	"strings"
	"sync"
	"testing"

	"go_module/sessionapi"
)

type releaseResultCallbacks struct {
	releaseOK bool
}

func (releaseResultCallbacks) AcquireTunnel(string, int64) int32          { return -1 }
func (c releaseResultCallbacks) ReleaseTunnel(string, int64, int32) bool  { return c.releaseOK }
func (releaseResultCallbacks) ProtectSocket(string, int64, int32) bool    { return true }
func (releaseResultCallbacks) PublishState(string, int64, string, string) {}

func TestTunnelLeaseReleasePropagatesPlatformCleanupFailure(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "mobile-tun")
	if err != nil {
		t.Fatal(err)
	}
	fd := int32(file.Fd())
	callbacks := releaseResultCallbacks{releaseOK: false}
	adapter := &platformAdapter{
		callbacks: callbacks,
		tunnels:   newTunnelFDs(),
		active:    make(map[string]sessionapi.SessionRef),
	}
	ref := sessionapi.SessionRef{SessionID: "session", Generation: 1}
	if !adapter.tunnels.reserve(fd, fdOwner{session: ref.SessionID, generation: ref.Generation}) {
		t.Fatal("test descriptor was unexpectedly already reserved")
	}
	lease := &tunnelLease{
		file:      file,
		fd:        fd,
		ref:       ref,
		adapter:   adapter,
		callbacks: callbacks,
	}

	if err := lease.Release(context.TODO()); err == nil || !strings.Contains(err.Error(), "platform tunnel cleanup failed") {
		t.Fatalf("Release error = %v, want platform cleanup failure", err)
	}
}

func TestPlatformRejectsCallbackValuesThatDoNotFitNativeTypes(t *testing.T) {
	callbacks := releaseResultCallbacks{releaseOK: true}
	adapter := &platformAdapter{
		callbacks: callbacks,
		tunnels:   newTunnelFDs(),
		active:    make(map[string]sessionapi.SessionRef),
	}
	if _, _, err := adapter.acquire(sessionapi.SessionRef{SessionID: "session", Generation: math.MaxUint64}); err == nil {
		t.Fatal("acquire accepted a generation that cannot fit the native callback type")
	}
	oversizedDescriptor := int(math.MaxInt32)
	oversizedDescriptor++
	if oversizedDescriptor > math.MaxInt32 {
		if err := adapter.ProtectSocket(context.Background(), sessionapi.SessionRef{Generation: 1}, oversizedDescriptor); err == nil {
			t.Fatal("socket protection accepted a descriptor that cannot fit the native callback type")
		}
	}
	adapter.active["session"] = sessionapi.SessionRef{SessionID: "session", Generation: math.MaxUint64}
	if adapter.protectActive(7) {
		t.Fatal("active socket protection accepted a generation that cannot fit the native callback type")
	}
}

func TestMissingPlatformCallbacksFailPredictably(t *testing.T) {
	adapter := &platformAdapter{
		tunnels: newTunnelFDs(),
		active:  make(map[string]sessionapi.SessionRef),
	}
	ref := sessionapi.SessionRef{SessionID: "session", Generation: 1}
	if _, _, err := adapter.acquire(ref); err == nil || err.Error() != "platform tunnel callback is not registered" {
		t.Fatalf("acquire without callback = %v", err)
	}
	if err := adapter.ProtectSocket(context.Background(), ref, 7); err == nil || err.Error() != "platform socket protector is not registered" {
		t.Fatalf("protect without callback = %v", err)
	}
	if adapter.protectActive(7) {
		t.Fatal("active protection succeeded without a callback")
	}
}

func TestCallbackReplacementKeepsManagerAndLeaseOwner(t *testing.T) {
	first := &trackingCallbacks{acquireFD: 41}
	second := &trackingCallbacks{acquireFD: 42}
	adapter := &platformAdapter{
		callbacks: first,
		tunnels:   newTunnelFDs(),
		active:    make(map[string]sessionapi.SessionRef),
	}
	manager := sessionapi.NewManager(sessionapi.ManagerOptions{Platform: adapter})
	binding := &Binding{manager: manager, platform: adapter}
	ref := sessionapi.SessionRef{SessionID: "session", Generation: 1}

	fd, acquiredWith, err := adapter.acquire(ref)
	if err != nil {
		t.Fatal(err)
	}
	binding.SetPlatformCallbacks(second)
	if binding.manager != manager {
		t.Fatal("callback replacement replaced the session manager")
	}
	if err := adapter.release(ref, fd, acquiredWith); err != nil {
		t.Fatal(err)
	}
	if first.releaseCount() != 1 || second.releaseCount() != 0 {
		t.Fatalf("release callbacks: first=%d second=%d", first.releaseCount(), second.releaseCount())
	}
}

type trackingCallbacks struct {
	mu        sync.Mutex
	acquireFD int32
	releases  int
}

func (c *trackingCallbacks) AcquireTunnel(string, int64) int32 { return c.acquireFD }
func (c *trackingCallbacks) ReleaseTunnel(string, int64, int32) bool {
	c.mu.Lock()
	c.releases++
	c.mu.Unlock()
	return true
}
func (*trackingCallbacks) ProtectSocket(string, int64, int32) bool    { return true }
func (*trackingCallbacks) PublishState(string, int64, string, string) {}
func (c *trackingCallbacks) releaseCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.releases
}
