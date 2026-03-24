//go:build darwin
// +build darwin

package internal

import (
	"fmt"
	"github.com/Jigsaw-Code/outline-sdk/network"
	"go_client/log"
	"os"
	"os/exec"
	"sync"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"

// ВАЖНО: разные IP!
const (
	tunLocalIP = "169.254.19.1"
	tunPeerIP  = "169.254.19.2"
	tunMask    = "255.255.255.0"
)

type tunDevice struct {
	file *os.File
	name string
	fd   int

	closeOnce sync.Once
}

var _ network.IPDevice = (*tunDevice)(nil)

func newTunDevice(name, ip string) (network.IPDevice, error) {
	log.Infof("[TUN] ====== START newTunDevice ======")

	fd, ifName, err := createUTUN(name)
	if err != nil {
		return nil, err
	}

	if fd <= 0 {
		return nil, fmt.Errorf("invalid fd: %d", fd)
	}

	file := os.NewFile(uintptr(fd), ifName)
	if file == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("failed to wrap fd")
	}

	tun := &tunDevice{
		file: file,
		name: ifName,
		fd:   fd,
	}

	log.Infof("[TUN] created %s (fd=%d)", ifName, fd)

	// BEFORE
	outBefore, _ := exec.Command("ifconfig", ifName).CombinedOutput()
	log.Infof("[TUN] BEFORE:\n%s", string(outBefore))

	// КРИТИЧЕСКОЕ ИСПРАВЛЕНИЕ
	cmd := exec.Command(
		"ifconfig",
		ifName,
		"inet",
		tunLocalIP,
		tunPeerIP,
		"netmask",
		tunMask,
		"up",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = tun.Close()
		return nil, fmt.Errorf("ifconfig failed: %w (%s)", err, string(out))
	}

	log.Infof("[TUN] configured %s: %s -> %s", ifName, tunLocalIP, tunPeerIP)

	// AFTER
	outAfter, _ := exec.Command("ifconfig", ifName).CombinedOutput()
	log.Infof("[TUN] AFTER:\n%s", string(outAfter))

	log.Infof("[TUN] ====== SUCCESS ======")
	return tun, nil
}

func createUTUN(name string) (int, string, error) {
	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, 2)
	if err != nil {
		return -1, "", err
	}

	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], []byte(utunControlName))

	if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
		unix.Close(fd)
		return -1, "", err
	}

	sc := &unix.SockaddrCtl{
		ID: ctlInfo.Id,
	}

	if err := unix.Connect(fd, sc); err != nil {
		unix.Close(fd)
		return -1, "", err
	}

	ifName, err := unix.GetsockoptString(fd, 2, 2)
	if err != nil {
		unix.Close(fd)
		return -1, "", err
	}

	return fd, ifName, nil
}

func (t *tunDevice) Read(p []byte) (int, error) {
	return t.file.Read(p)
}

func (t *tunDevice) Write(p []byte) (int, error) {
	return t.file.Write(p)
}

func (t *tunDevice) Close() error {
	var err error
	t.closeOnce.Do(func() {
		log.Infof("[TUN] closing %s (fd=%d)", t.name, t.fd)
		err = t.file.Close()
	})
	return err
}

func (t *tunDevice) MTU() int {
	return 1500
}

func (t *tunDevice) GetFd() int {
	return t.fd
}
