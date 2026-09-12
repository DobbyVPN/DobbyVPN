//go:build android || ios

package mobilebinding

import (
	"os"
	"strings"
	"testing"

	v2 "go_module/sessionapi/v2"
)

type releaseResultCallbacks struct {
	releaseOK bool
}

func (releaseResultCallbacks) AcquireTunnel(string, int64) int32                                { return -1 }
func (c releaseResultCallbacks) ReleaseTunnel(string, int64, int32) bool                        { return c.releaseOK }
func (releaseResultCallbacks) ProtectSocket(string, int64, int32) bool                          { return true }
func (releaseResultCallbacks) PublishState(string, int64, int64, string, int32, string, string) {}

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
		active:    make(map[string]v2.SessionRef),
	}
	ref := v2.SessionRef{SessionID: "session", Generation: 1}
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

	if err := lease.Release(nil); err == nil || !strings.Contains(err.Error(), "platform tunnel cleanup failed") {
		t.Fatalf("Release error = %v, want platform cleanup failure", err)
	}
}
