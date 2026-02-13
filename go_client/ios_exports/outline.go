//go:build android || ios

package cloak_outline

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"unsafe"

	"go_client/common"
	log "go_client/logger"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/lwip2transport"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"

var providers = configurl.NewDefaultProviders()

// ------------------ GLOBAL STATE ------------------

var (
	clientMu sync.Mutex
	client   *OutlineDevice
)

// ------------------ UTILS ------------------

func guardExport(fn string) func() {
	return func() {
		if r := recover(); r != nil {
			log.Infof("panic in %s: %v\n%s", fn, r, string(debug.Stack()))
		}
	}
}

// ------------------ DEVICE ------------------

type OutlineDevice struct {
	network.IPDevice
	sd    transport.StreamDialer
	pp    *outlinePacketProxy
	svrIP net.IP
}

// ------------------ CLIENT INIT ------------------

func NewOutlineClient(config string) error {
	defer guardExport("NewOutlineClient")()

	config = strings.TrimSpace(config)
	if config == "" {
		return errors.New("empty config")
	}

	ip, err := resolveShadowsocksServerIPFromConfig(config)
	if err != nil {
		return err
	}

	od := &OutlineDevice{svrIP: ip}

	if od.sd, err = providers.NewStreamDialer(context.Background(), config); err != nil {
		return fmt.Errorf("create stream dialer: %w", err)
	}

	if od.pp, err = newOutlinePacketProxy(config); err != nil {
		return fmt.Errorf("create packet proxy: %w", err)
	}

	if od.IPDevice, err = lwip2transport.ConfigureDevice(od.sd, od.pp); err != nil {
		return fmt.Errorf("configure lwip: %w", err)
	}

	clientMu.Lock()
	defer clientMu.Unlock()

	// если уже был клиент — корректно отключим
	if client != nil {
		_ = OutlineDisconnect()
	}

	client = od
	log.Infof("NewOutlineClient(): initialized, serverIP=%v", ip)

	return nil
}

// ------------------ CONNECT ------------------

func OutlineConnect() error {
	defer guardExport("OutlineConnect")()

	clientMu.Lock()
	od := client
	clientMu.Unlock()

	if od == nil || od.IPDevice == nil {
		return errors.New("client not initialized")
	}

	fd := GetTunnelFileDescriptor()
	if fd < 0 {
		return errors.New("utun fd not found")
	}

	ifName, err := getUtunIfName(fd)
	if err != nil {
		log.Infof("utun name lookup failed: %v", err)
	} else {
		log.Infof("utun fd=%d name=%s", fd, ifName)
	}

	common.StartTransferDarwin(
		fd,
		func(p []byte) (int, error) {
			return od.IPDevice.Read(p)
		},
		func(p []byte) (int, error) {
			return od.IPDevice.Write(p)
		},
	)

	log.Infof("OutlineConnect(): transfer started")
	return nil
}

// ------------------ DISCONNECT ------------------

func OutlineDisconnect() error {
	defer guardExport("OutlineDisconnect")()

	common.StopTransferDarwin()

	clientMu.Lock()
	defer clientMu.Unlock()

	if client != nil && client.IPDevice != nil {
		_ = client.IPDevice.Close()
	}

	client = nil

	log.Infof("OutlineDisconnect(): done")
	return nil
}

// ------------------ UTUN FD SEARCH ------------------

func GetTunnelFileDescriptor() int {
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], utunControlName)

	for fd := 0; fd < 1024; fd++ {
		addr, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}

		addrCtl, ok := addr.(*unix.SockaddrCtl)
		if !ok {
			continue
		}

		if ctlInfo.Id == 0 {
			if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
				continue
			}
		}

		if addrCtl.ID == ctlInfo.Id {
			return fd
		}
	}
	return -1
}

// ------------------ UTUN NAME ------------------

func getUtunIfName(fd int) (string, error) {
	const (
		sysProtoControl = 2
		utunOptIfName   = 2
	)

	var name [32]byte
	size := uint32(len(name))

	_, _, errno := unix.Syscall6(
		unix.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(sysProtoControl),
		uintptr(utunOptIfName),
		uintptr(unsafe.Pointer(&name[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
	)

	if errno != 0 {
		return "", errno
	}

	if size == 0 {
		return "", nil
	}

	return string(name[:size-1]), nil
}

// ------------------ CONFIG PARSING ------------------

func extractTLSSNIHost(config string) string {
	parts := strings.Split(config, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "tls:") {
			params := strings.TrimPrefix(part, "tls:")
			for _, p := range strings.Split(params, "&") {
				if strings.HasPrefix(p, "sni=") {
					return strings.TrimPrefix(p, "sni=")
				}
			}
		}
	}
	return ""
}

func resolveShadowsocksServerIPFromConfig(config string) (net.IP, error) {
	config = strings.TrimSpace(config)
	if config == "" {
		return nil, errors.New("config required")
	}

	var host string

	if sni := extractTLSSNIHost(config); sni != "" {
		host = sni
	} else {
		var ssPart string
		for _, p := range strings.Split(config, "|") {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "ss://") {
				ssPart = p
				break
			}
		}
		if ssPart == "" {
			return nil, errors.New("ss:// part not found")
		}
		u, err := url.Parse(ssPart)
		if err != nil {
			return nil, err
		}
		host = u.Hostname()
	}

	if host == "127.0.0.1" || host == "localhost" {
		return net.ParseIP("127.0.0.1").To4(), nil
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}

	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
	}

	return nil, errors.New("IPv6-only server not supported")
}
