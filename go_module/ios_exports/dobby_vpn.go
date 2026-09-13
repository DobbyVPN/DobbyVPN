//go:build ios

package dobbyvpn

import (
	"sync"

	"go_module/sessionapi/mobilebinding"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"
const logCategory = "ios_exports"

var (
	iosCallbacks   iosPlatformCallbacks
	mobileSessions = mobilebinding.New(&iosCallbacks)
)

// PlatformCallbacks is declared in the bound package so gobind emits the
// Objective-C protocol instead of skipping an interface imported from another
// Go package.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) int32
	ReleaseTunnel(sessionID string, generation int64, fd int32) bool
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(
		sessionID string,
		generation int64,
		state string,
		failureCode string,
	)
}

// RegisterSessionPlatform installs the NetworkExtension boundary used by the
// shared runtime.
func RegisterSessionPlatform(callbacks PlatformCallbacks) {
	iosCallbacks.set(callbacks)
}

func ConfigureSession(sessionID string, sequence int64, rawConfig []byte) string {
	return mobileSessions.Configure(sessionID, sequence, rawConfig)
}
func StartSession(sessionID string, sequence int64, mode string, index int32) string {
	return mobileSessions.Start(sessionID, sequence, mode, index)
}
func StopSession(sessionID string, generation int64) string {
	return mobileSessions.Stop(sessionID, generation)
}
func SnapshotSession(sessionID string) string { return mobileSessions.Snapshot(sessionID) }
func ResetSession(sessionID string, sequence int64) string {
	return mobileSessions.Reset(sessionID, sequence)
}

type iosPlatformCallbacks struct {
	mu       sync.RWMutex
	delegate mobilebinding.PlatformCallbacks
}

func (p *iosPlatformCallbacks) set(callbacks mobilebinding.PlatformCallbacks) {
	p.mu.Lock()
	p.delegate = callbacks
	p.mu.Unlock()
}
func (p *iosPlatformCallbacks) callback() mobilebinding.PlatformCallbacks {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.delegate
}
func (p *iosPlatformCallbacks) AcquireTunnel(sessionID string, generation int64) int32 {
	if callback := p.callback(); callback != nil {
		return callback.AcquireTunnel(sessionID, generation)
	}
	return -1
}
func (p *iosPlatformCallbacks) ReleaseTunnel(sessionID string, generation int64, fd int32) bool {
	if callback := p.callback(); callback != nil {
		return callback.ReleaseTunnel(sessionID, generation, fd)
	}
	return false
}
func (p *iosPlatformCallbacks) ProtectSocket(sessionID string, generation int64, fd int32) bool {
	if callback := p.callback(); callback != nil {
		return callback.ProtectSocket(sessionID, generation, fd)
	}
	return false
}
func (p *iosPlatformCallbacks) PublishState(sessionID string, generation int64, state string, failureCode string) {
	if callback := p.callback(); callback != nil {
		callback.PublishState(sessionID, generation, state, failureCode)
	}
}

func GetTunnelFileDescriptor() int {
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], utunControlName)
	for fd := 0; fd < 1024; fd++ {
		addr, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}
		addrCTL, ok := addr.(*unix.SockaddrCtl)
		if !ok {
			continue
		}
		if ctlInfo.Id == 0 && unix.IoctlCtlInfo(fd, ctlInfo) != nil {
			continue
		}
		if addrCTL.ID == ctlInfo.Id {
			return fd
		}
	}
	return -1
}
