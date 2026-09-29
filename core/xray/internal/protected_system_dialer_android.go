//go:build android

package internal

import (
	"core/tunnel/protected_dialer"

	"github.com/xtls/xray-core/transport/internet"
)

func newAndroidProtectedSystemDialer() *protectedSystemDialer {
	return newProtectedSystemDialer(protected_dialer.ProtectRawConn, applyPlatformOutboundSocketOptions)
}

func init() {
	// Install once at Android process startup. The protector callback resolves
	// the active VpnService/session generation for each new socket, so this
	// process-global dialer does not retain a session callback across restarts.
	internet.UseAlternativeSystemDialer(newAndroidProtectedSystemDialer())
}
