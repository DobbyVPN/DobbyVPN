//go:build linux

package platform_engine

import (
	"strconv"
	"testing"

	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
	"golang.org/x/sys/unix"
)

func TestTun2SocksFDDeviceCloseDoesNotCloseReusedDescriptor(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("create socket pair: %v", err)
	}
	defer unix.Close(pair[1])

	device, err := fdbased.Open(strconv.Itoa(pair[0]), 1200, 0)
	if err != nil {
		unix.Close(pair[0])
		t.Fatalf("open fd device: %v", err)
	}
	device.Close()

	replacement, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open replacement descriptor: %v", err)
	}
	if replacement != pair[0] {
		defer unix.Close(replacement)
		if err := unix.Dup3(replacement, pair[0], unix.O_CLOEXEC); err != nil {
			t.Fatalf("reuse closed device descriptor: %v", err)
		}
	}
	defer unix.Close(pair[0])

	device.Close()
	if _, err := unix.FcntlInt(uintptr(pair[0]), unix.F_GETFD, 0); err != nil {
		t.Fatalf("second device close closed a reused descriptor: %v", err)
	}
}
