//go:build windows

package controlplane

import (
	"context"
	"fmt"
	"net"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const desktopControlPipeName = `\\.\pipe\DobbyVPN.Control`

// DesktopControlPipeName returns the fixed native desktop endpoint.
func DesktopControlPipeName() string {
	return desktopControlPipeName
}

func DialDesktopControl(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeContext(ctx, DesktopControlPipeName())
}

func ListenDesktopControlPipe() (net.Listener, error) {
	account, err := installedControlUser()
	if err != nil {
		return nil, err
	}
	userSID, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return nil, fmt.Errorf("resolve installed-user SID for desktop control: %w", err)
	}
	systemSID, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return nil, err
	}
	if userSID.Equals(systemSID) {
		return nil, fmt.Errorf("installed desktop user cannot be SYSTEM")
	}
	sddl := fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GRGW;;;%s)", userSID.String())
	// go-winio sets PIPE_REJECT_REMOTE_CLIENTS on every pipe instance.
	return winio.ListenPipe(desktopControlPipeName, &winio.PipeConfig{SecurityDescriptor: sddl})
}
