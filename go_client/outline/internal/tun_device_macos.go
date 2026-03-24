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

type tunDevice struct {
	file *os.File
	name string
	fd   int

	closeOnce sync.Once
}

var _ network.IPDevice = (*tunDevice)(nil)

func newTunDevice(name, ip string) (network.IPDevice, error) {
	log.Infof("[TUN] ====== START newTunDevice ======")
	log.Infof("[TUN] requested name=%s ip=%s", name, ip)

	fd, ifName, err := createUTUN(name)
	if err != nil {
		log.Infof("[TUN] createUTUN FAILED: %v", err)
		return nil, err
	}

	log.Infof("[TUN] createUTUN OK: fd=%d ifName=%s", fd, ifName)

	if fd <= 0 {
		return nil, fmt.Errorf("invalid fd: %d", fd)
	}

	file := os.NewFile(uintptr(fd), ifName)
	if file == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("failed to wrap fd into os.File")
	}

	tun := &tunDevice{
		file: file,
		name: ifName,
		fd:   fd,
	}

	log.Infof("[TUN] created %s (fd=%d)", ifName, fd)

	cmdCheck := exec.Command("ifconfig", ifName)
	outCheck, _ := cmdCheck.CombinedOutput()
	log.Infof("[TUN] ifconfig BEFORE:\n%s", string(outCheck))

	log.Infof("[TUN] configuring interface %s as 169.254 link-local...", ifName)

	cmd := exec.Command(
		"ifconfig",
		ifName,
		"inet",
		"169.254.19.0",
		"169.254.19.0",
		"netmask",
		"255.255.255.0",
		"up",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = tun.Close()
		log.Infof("[TUN] ifconfig FAILED: %s", string(out))
		return nil, fmt.Errorf("ifconfig failed: %w (%s)", err, string(out))
	}

	log.Infof("[TUN] ifconfig OK: %s", string(out))

	cmdCheck2 := exec.Command("ifconfig", ifName)
	outCheck2, _ := cmdCheck2.CombinedOutput()
	log.Infof("[TUN] ifconfig AFTER:\n%s", string(outCheck2))

	log.Infof("[TUN] ====== SUCCESS newTunDevice ======")
	return tun, nil
}

func createUTUN(name string) (int, string, error) {

	log.Infof("[UTUN] creating utun...")

	fd, err := unix.Socket(unix.AF_SYSTEM, unix.SOCK_DGRAM, 2)
	if err != nil {
		log.Infof("[UTUN] socket FAILED: %v", err)
		return -1, "", err
	}
	log.Infof("[UTUN] socket OK fd=%d", fd)

	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], []byte(utunControlName))

	log.Infof("[UTUN] ioctl ctl info...")

	if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
		log.Infof("[UTUN] IoctlCtlInfo FAILED: %v", err)
		unix.Close(fd)
		return -1, "", err
	}

	log.Infof("[UTUN] ctlInfo.Id=%d", ctlInfo.Id)

	sc := &unix.SockaddrCtl{
		ID: ctlInfo.Id,
	}

	log.Infof("[UTUN] connecting to kernel control...")

	if err := unix.Connect(fd, sc); err != nil {
		log.Infof("[UTUN] connect FAILED: %v", err)
		unix.Close(fd)
		return -1, "", err
	}

	log.Infof("[UTUN] connect OK")

	ifName, err := unix.GetsockoptString(
		fd,
		2,
		2, // UTUN_OPT_IFNAME
	)
	if err != nil {
		log.Infof("[UTUN] GetsockoptString FAILED: %v", err)
		unix.Close(fd)
		return -1, "", err
	}

	log.Infof("[UTUN] interface name = %s", ifName)

	log.Infof("[UTUN] ====== SUCCESS createUTUN ======")

	return fd, ifName, nil
}

func (t *tunDevice) Read(p []byte) (int, error) {
	n, err := t.file.Read(p)
	if err != nil {
		log.Infof("[TUN] READ error: %v", err)
		return n, err
	}

	if n > 0 {
		log.Infof("[TUN] READ %d bytes", n)
	}

	return n, nil
}

func (t *tunDevice) Write(p []byte) (int, error) {
	n, err := t.file.Write(p)
	if err != nil {
		log.Infof("[TUN] WRITE error: %v", err)
		return n, err
	}

	if n > 0 {
		log.Infof("[TUN] WRITE %d bytes", n)
	}

	return n, nil
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
	log.Infof("[TUN] GetFd called -> %d", t.fd)
	return t.fd
}
