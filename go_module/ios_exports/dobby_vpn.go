//go:build ios

package dobbyvpn

import (
	"sync"
	"syscall"

	"go_module/sessionapi/mobilebinding"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"
const logCategory = "ios_exports"
const soNoTCNetPolicy = 0x1101

var (
	iosCallbacks   iosPlatformCallbacks
	mobileSessions = mobilebinding.New(&iosCallbacks)
)

// PlatformCallbacks is declared in the bound package so gobind emits the
// Objective-C protocol instead of skipping an interface imported from another
// Go package.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) int32
	ReleaseTunnel(sessionID string, generation int64, fd int32)
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(
		sessionID string,
		generation int64,
		sequence int64,
		state string,
		profileIndex int32,
		profileProtocol string,
		failureCode string,
	)
}

// RegisterSessionPlatform lets the NetworkExtension shell receive only safe,
// generation-correlated state. Without a delegate, iOS acquires its TUN by
// locating and duplicating the current utun for every runtime generation.
func RegisterSessionPlatform(callbacks PlatformCallbacks) {
	iosCallbacks.set(callbacks)
}

func GetSessionCapabilities() string { return mobileSessions.GetCapabilities() }
func CreateSession() string          { return mobileSessions.CreateSession() }
func RecoverActiveSession() string   { return mobileSessions.RecoverActiveSession() }
func ConfigureSession(sessionID, commandID string, rawConfig []byte) string {
	return mobileSessions.Configure(sessionID, commandID, rawConfig)
}
func StartSession(sessionID, commandID, mode string, index int32) string {
	return mobileSessions.Start(sessionID, commandID, mode, index)
}
func StopSession(sessionID, commandID string, generation int64) string {
	return mobileSessions.Stop(sessionID, commandID, generation)
}
func SnapshotSession(sessionID string) string { return mobileSessions.Snapshot(sessionID) }
func ObserveSession(sessionID string, afterSequence int64) string {
	return mobileSessions.Observe(sessionID, afterSequence)
}
func DestroySession(sessionID string) string { return mobileSessions.Destroy(sessionID) }

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
	fd := GetTunnelFileDescriptor()
	if fd < 0 {
		return -1
	}
	dup, err := unix.Dup(fd)
	if err != nil {
		return -1
	}
	return int32(dup)
}
func (p *iosPlatformCallbacks) ReleaseTunnel(sessionID string, generation int64, fd int32) {
	if callback := p.callback(); callback != nil {
		callback.ReleaseTunnel(sessionID, generation, fd)
	}
}
func (p *iosPlatformCallbacks) ProtectSocket(sessionID string, generation int64, fd int32) bool {
	if callback := p.callback(); callback != nil {
		return callback.ProtectSocket(sessionID, generation, fd)
	}
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soNoTCNetPolicy, 1) == nil
}
func (p *iosPlatformCallbacks) PublishState(sessionID string, generation int64, sequence int64, state string, profileIndex int32, profileProtocol string, failureCode string) {
	if callback := p.callback(); callback != nil {
		callback.PublishState(sessionID, generation, sequence, state, profileIndex, profileProtocol, failureCode)
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
