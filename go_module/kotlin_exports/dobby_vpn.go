//go:build android

package dobbyvpn

import (
	"strings"
	"sync"

	"go_module/log"
	"go_module/sessionapi/mobilebinding"
	"go_module/vpnmanager"
)

const logCategory = "kotlin_exports"

var (
	lastError        = vpnmanager.NewLastError(logCategory)
	androidCallbacks androidPlatformCallbacks
	mobileSessions   = mobilebinding.New(&androidCallbacks)
	legacySession    = mobileSessions.NewLegacyClient()
)

// PlatformCallbacks is declared in the bound package so gobind emits the Java
// interface. Interfaces imported from another Go package are skipped by
// gomobile even when all of their methods use supported types.
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

// RegisterSessionPlatform installs the narrow Android VpnService boundary for
// sessionapi. The callback acquires a fresh, already-duplicated TUN per
// generation and receives all correlated state notifications.
func RegisterSessionPlatform(callbacks PlatformCallbacks) {
	androidCallbacks.set(callbacks)
}

// Session API bindings. Each result is a safe JSON envelope and never echoes
// a raw configuration, URL, or credential.
func GetSessionCapabilities() string { return mobileSessions.GetCapabilities() }
func CreateSession() string          { return mobileSessions.CreateSession() }
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

func GetVpnLastError() string { return lastError.Get() }

// NewVpnClient is retained for source compatibility only. It prepares a v1
// compatibility profile and transfers fd to exactly one future generation; it
// never switches a live protocol client.
func NewVpnClient(config string, protocol string, fd int32) {
	defer vpnmanager.GuardExport(logCategory, "NewVpnClient", lastError)()
	lastError.Clear()
	if err := legacySession.Configure(strings.Clone(config), strings.Clone(protocol), fd); err != nil {
		lastError.Set(err.Error())
		log.Debugf(logCategory, "legacy configure failed")
		return
	}
	log.Debugf(logCategory, "legacy session configured")
}

func VpnConnect() int32 {
	defer vpnmanager.GuardExport(logCategory, "VpnConnect", lastError)()
	if pendingErr := lastError.Get(); pendingErr != "" {
		return -1
	}
	lastError.Clear()
	if err := legacySession.Connect(); err != nil {
		lastError.Set(err.Error())
		log.Debugf(logCategory, "legacy start rejected")
		return -1
	}
	log.Debugf(logCategory, "legacy start requested")
	return 0
}

func VpnDisconnect() {
	defer vpnmanager.GuardExport(logCategory, "VpnDisconnect", lastError)()
	if err := legacySession.Disconnect(); err != nil {
		lastError.Set(err.Error())
		log.Debugf(logCategory, "legacy stop failed")
		return
	}
	lastError.Clear()
	log.Debugf(logCategory, "legacy session stopped")
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
func (p *androidPlatformCallbacks) ReleaseTunnel(sessionID string, generation int64, fd int32) {
	if callback := p.callback(); callback != nil {
		callback.ReleaseTunnel(sessionID, generation, fd)
	}
}
func (p *androidPlatformCallbacks) ProtectSocket(sessionID string, generation int64, fd int32) bool {
	if callback := p.callback(); callback != nil {
		return callback.ProtectSocket(sessionID, generation, fd)
	}
	return protectSocketFallback(fd)
}
func (p *androidPlatformCallbacks) PublishState(sessionID string, generation int64, sequence int64, state string, profileIndex int32, profileProtocol string, failureCode string) {
	if callback := p.callback(); callback != nil {
		callback.PublishState(sessionID, generation, sequence, state, profileIndex, profileProtocol, failureCode)
	}
}
