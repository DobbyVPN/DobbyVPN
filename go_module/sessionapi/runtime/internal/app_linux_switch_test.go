//go:build linux && !(android || ios)

package internal

import (
	"net"
	"strings"
	"testing"
)

type rejectedSwitchDevice struct {
	closeCalls int
}

func (d *rejectedSwitchDevice) Open(int, string) error { return nil }
func (d *rejectedSwitchDevice) GetProxyAddr() string   { return "" }
func (d *rejectedSwitchDevice) GetServerIP() net.IP    { return net.IPv4(127, 0, 0, 1) }
func (d *rejectedSwitchDevice) Close() error {
	d.closeCalls++
	return nil
}

func TestLinuxSwitchProtocolDeviceRequiresFullStop(t *testing.T) {
	replacement := &rejectedSwitchDevice{}
	err := (&App{}).SwitchProtocolDevice(replacement)
	if err == nil || !strings.Contains(err.Error(), "stop the active session") {
		t.Fatalf("SwitchProtocolDevice error = %v", err)
	}
	if replacement.closeCalls != 1 {
		t.Fatalf("replacement Close calls = %d, want 1", replacement.closeCalls)
	}
}
