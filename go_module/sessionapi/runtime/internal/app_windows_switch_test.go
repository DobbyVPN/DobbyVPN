//go:build windows && !(android || ios)

package internal

import (
	"net"
	"strings"
	"testing"
)

type rejectedWindowsSwitchDevice struct {
	closeCalls int
}

func (d *rejectedWindowsSwitchDevice) Open(int, string) error { return nil }
func (d *rejectedWindowsSwitchDevice) GetProxyAddr() string   { return "" }
func (d *rejectedWindowsSwitchDevice) GetServerIP() net.IP    { return net.IPv4(127, 0, 0, 1) }
func (d *rejectedWindowsSwitchDevice) Close() error {
	d.closeCalls++
	return nil
}

func TestWindowsSwitchProtocolDeviceRequiresFullStopAndClosesReplacement(t *testing.T) {
	replacement := &rejectedWindowsSwitchDevice{}
	err := (&App{}).SwitchProtocolDevice(replacement)
	if err == nil || !strings.Contains(err.Error(), "stop the active session") {
		t.Fatalf("SwitchProtocolDevice error = %v", err)
	}
	if replacement.closeCalls != 1 {
		t.Fatalf("replacement Close calls = %d, want 1", replacement.closeCalls)
	}
}
