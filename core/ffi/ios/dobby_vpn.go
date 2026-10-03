//go:build ios

package dobbyvpn

import (
	"context"
	"encoding/json"

	"core/sessionapi/mobilebinding"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"
const logCategory = "ios_exports"

var mobileSessions = mobilebinding.New(nil)

// PlatformCallbacks is declared in the bound package so gobind emits the
// Objective-C protocol instead of skipping an interface imported from another
// Go package.
type PlatformCallbacks interface {
	AcquireTunnel(sessionID string, generation int64) string
	ReleaseTunnel(sessionID string, generation int64, fd int32, timeoutMillis int64) string
	ProtectSocket(sessionID string, generation int64, fd int32) bool
	PublishState(
		sessionID string,
		generation int64,
		state string,
		failureCode string,
	)
	LoadSourceURL() string
	SaveSourceURL(value string) bool
	ClearSourceURL() bool
}

// RegisterSessionPlatform installs the NetworkExtension boundary used by the
// shared runtime.
func RegisterSessionPlatform(callbacks PlatformCallbacks) string {
	if err := mobileSessions.Resume(); err != nil {
		return err.Error()
	}
	mobileSessions.SetPlatformCallbacks(callbacks)
	mobileSessions.AttachSourceStore()
	return ""
}

// CallSessionJSON accepts the same method/params command used by desktop
// control. iOS configuration bytes arrive separately from the NetworkExtension
// message through the request-scoped Keychain mailbox.
func CallSessionJSON(method string, params []byte, rawConfiguration []byte, hasConfiguration bool) string {
	return mobileSessions.CallJSONWithConfiguration(
		context.Background(), method, json.RawMessage(params), rawConfiguration, hasConfiguration,
	)
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

// StopSessionAndWait is the provider lifecycle boundary, separate from the
// public command protocol. Callbacks stay registered until release completes.
func StopSessionAndWait() string {
	if err := mobileSessions.StopAndWait(context.Background()); err != nil {
		return err.Error()
	}
	mobileSessions.SetPlatformCallbacks(nil)
	return ""
}
