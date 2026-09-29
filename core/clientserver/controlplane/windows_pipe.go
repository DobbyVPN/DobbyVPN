//go:build windows

package controlplane

import (
	"context"
	"fmt"
	"net"

	winio "github.com/Microsoft/go-winio"
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
	userSID, err := installedControlUserSID()
	if err != nil {
		return nil, err
	}
	sddl := fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GRGW;;;%s)", userSID.String())
	// go-winio sets PIPE_REJECT_REMOTE_CLIENTS on every pipe instance.
	return winio.ListenPipe(desktopControlPipeName, &winio.PipeConfig{SecurityDescriptor: sddl})
}
