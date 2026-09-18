//go:build android

package dobbyvpn

import (
	"go_module/sessionapi/mobilebinding"
)

var mobileSessions = mobilebinding.New(nil)

// PlatformCallbacks is declared in the bound package so gobind emits the Java
// interface instead of skipping an interface imported from another package.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) int32
	ReleaseTunnel(sessionID string, generation int64, fd int32) bool
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(sessionID string, generation int64, state string, failureCode string)
}

// RegisterSessionPlatform installs the narrow Android VpnService boundary.
func RegisterSessionPlatform(callbacks PlatformCallbacks) {
	mobileSessions.SetPlatformCallbacks(callbacks)
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
