//go:build !linux && !darwin && !(windows || android || ios)

package controlplane

import (
	"errors"
	"net"
	"os"
)

func peerUID(net.Conn) (int, error) { return 0, errWrongLocalConnection }
func currentUID() int               { return os.Getuid() }
func privilegedDefaultPeerUID() (int, error) {
	return 0, errors.New("privileged peer discovery is unsupported on this platform")
}
