//go:build android

package dobbyvpn

import (
	"go_module/sessionapi/mobilebinding"
)

var mobileSessions = mobilebinding.New(nil)

func init() { installJNIPlatform(mobileSessions) }

// SetAndroidContext gives the JNI callback adapter the activity context and VM
// pointers needed to call the native VPN shell.
func SetAndroidContext(vm, env, context uintptr) { setAndroidContext(vm, env, context) }

// PrepareAndroidService requests Android VPN consent when needed and starts
// the foreground service once permission is already granted. Return 1 when
// ready, 0 when consent was launched and -1 for a native bridge failure.
func PrepareAndroidService() int { return prepareAndroidService() }

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
