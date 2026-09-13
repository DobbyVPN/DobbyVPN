//go:build android

package dobbyvpn

import (
	"sync"

	"go_module/sessionapi/mobilebinding"
)

var (
	androidCallbacks androidPlatformCallbacks
	mobileSessions   = mobilebinding.New(&androidCallbacks)
)

// PlatformCallbacks is declared in the bound package so gobind emits the Java
// interface instead of skipping an interface imported from another package.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) int32
	ReleaseTunnel(sessionID string, generation int64, fd int32) bool
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(sessionID string, generation int64, state string, failureCode string)
}

// RegisterSessionPlatform installs the narrow Android VpnService boundary.
func RegisterSessionPlatform(callbacks PlatformCallbacks) { androidCallbacks.set(callbacks) }

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

type androidPlatformCallbacks struct {
	mu       sync.RWMutex
	delegate mobilebinding.PlatformCallbacks
}

func (p *androidPlatformCallbacks) set(callbacks mobilebinding.PlatformCallbacks) {
	p.mu.Lock()
	p.delegate = callbacks
	p.mu.Unlock()
}

func (p *androidPlatformCallbacks) callback() mobilebinding.PlatformCallbacks {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.delegate
}

func (p *androidPlatformCallbacks) AcquireTunnel(sessionID string, generation int64) int32 {
	if callback := p.callback(); callback != nil {
		return callback.AcquireTunnel(sessionID, generation)
	}
	return -1
}

func (p *androidPlatformCallbacks) ReleaseTunnel(sessionID string, generation int64, fd int32) bool {
	if callback := p.callback(); callback != nil {
		return callback.ReleaseTunnel(sessionID, generation, fd)
	}
	return false
}

func (p *androidPlatformCallbacks) ProtectSocket(sessionID string, generation int64, fd int32) bool {
	if callback := p.callback(); callback != nil {
		return callback.ProtectSocket(sessionID, generation, fd)
	}
	return false
}

func (p *androidPlatformCallbacks) PublishState(sessionID string, generation int64, state string, failureCode string) {
	if callback := p.callback(); callback != nil {
		callback.PublishState(sessionID, generation, state, failureCode)
	}
}
