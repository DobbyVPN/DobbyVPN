//go:build linux || android

package internal

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/xtls/xray-core/transport/internet"
	"golang.org/x/sys/unix"
)

// applyPlatformOutboundSocketOptions follows the outbound subset of Xray
// v1.260327.0's Linux socket-option behavior. It is used by the Android
// protected system dialer because Xray's implementation is package-private.
func applyPlatformOutboundSocketOptions(network, _ string, fd uintptr, config *internet.SocketConfig) error {
	if config == nil {
		return nil
	}
	if config.Mark != 0 {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, int(config.Mark)); err != nil {
			return fmt.Errorf("failed to set SO_MARK: %w", err)
		}
	}
	if config.Interface != "" {
		if err := syscall.BindToDevice(int(fd), config.Interface); err != nil {
			return fmt.Errorf("failed to set Interface: %w", err)
		}
	}
	if strings.HasPrefix(network, "tcp") {
		tfo := config.ParseTFOValue()
		if tfo > 0 {
			tfo = 1
		}
		if tfo >= 0 {
			if err := syscall.SetsockoptInt(int(fd), syscall.SOL_TCP, unix.TCP_FASTOPEN_CONNECT, tfo); err != nil {
				return fmt.Errorf("failed to set TCP_FASTOPEN_CONNECT: %w", err)
			}
		}
		if config.TcpCongestion != "" {
			if err := syscall.SetsockoptString(int(fd), syscall.SOL_TCP, syscall.TCP_CONGESTION, config.TcpCongestion); err != nil {
				return fmt.Errorf("failed to set TCP_CONGESTION: %w", err)
			}
		}
		if config.TcpWindowClamp > 0 {
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_WINDOW_CLAMP, int(config.TcpWindowClamp)); err != nil {
				return fmt.Errorf("failed to set TCP_WINDOW_CLAMP: %w", err)
			}
		}
		if config.TcpUserTimeout > 0 {
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, int(config.TcpUserTimeout)); err != nil {
				return fmt.Errorf("failed to set TCP_USER_TIMEOUT: %w", err)
			}
		}
		if config.TcpMaxSeg > 0 {
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, unix.TCP_MAXSEG, int(config.TcpMaxSeg)); err != nil {
				return fmt.Errorf("failed to set TCP_MAXSEG: %w", err)
			}
		}
	}
	for _, custom := range config.CustomSockopt {
		if custom.System != "" && custom.System != runtime.GOOS {
			continue
		}
		if !strings.HasPrefix(network, custom.Network) {
			continue
		}
		if custom.Opt == "" {
			return fmt.Errorf("custom socket option has no opt")
		}
		option, _ := strconv.Atoi(custom.Opt)
		level := 0x6
		if custom.Level != "" {
			level, _ = strconv.Atoi(custom.Level)
		}
		switch custom.Type {
		case "int":
			value, _ := strconv.Atoi(custom.Value)
			if err := syscall.SetsockoptInt(int(fd), level, option, value); err != nil {
				return fmt.Errorf("failed to set CustomSockoptInt: %w", err)
			}
		case "str":
			if err := syscall.SetsockoptString(int(fd), level, option, custom.Value); err != nil {
				return fmt.Errorf("failed to set CustomSockoptString: %w", err)
			}
		default:
			return fmt.Errorf("unknown CustomSockopt type %q", custom.Type)
		}
	}
	if config.Tproxy.IsEnabled() {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_IP, syscall.IP_TRANSPARENT, 1); err != nil {
			return fmt.Errorf("failed to set IP_TRANSPARENT: %w", err)
		}
	}
	return nil
}

func bindPlatformUDPAddress(fd uintptr, address []byte, port uint32) error {
	// Match Xray's Linux bindAddr behavior. These options are best-effort, as
	// they are in the pinned default dialer.
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	var sockaddr syscall.Sockaddr
	switch len(address) {
	case 4:
		a4 := &syscall.SockaddrInet4{Port: int(port)}
		copy(a4.Addr[:], address)
		sockaddr = a4
	case 16:
		a6 := &syscall.SockaddrInet6{Port: int(port)}
		copy(a6.Addr[:], address)
		sockaddr = a6
	default:
		return fmt.Errorf("unexpected bind address length %d", len(address))
	}
	return syscall.Bind(int(fd), sockaddr)
}
