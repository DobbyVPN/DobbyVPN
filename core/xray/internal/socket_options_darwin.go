//go:build darwin || ios

package internal

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/xtls/xray-core/transport/internet"
	"golang.org/x/sys/unix"
)

// Socket options remain protocol-owned; routing protection runs after this
// adapter, so custom options cannot override the app's physical-interface bind.
func applyPlatformOutboundSocketOptions(network, _ string, fd uintptr, config *internet.SocketConfig) error {
	if config == nil {
		return nil
	}
	if strings.HasPrefix(network, "tcp") {
		tfo := config.ParseTFOValue()
		if tfo > 0 {
			tfo = 2
		} // TCP_FASTOPEN_CLIENT
		if tfo >= 0 {
			if err := unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_FASTOPEN, tfo); err != nil {
				return err
			}
		}
		// Keepalive values are applied by net.Dialer.KeepAliveConfig.
	}
	if config.Interface != "" {
		iface, err := net.InterfaceByName(config.Interface)
		if err != nil {
			return err
		}
		level, option := unix.IPPROTO_IP, unix.IP_BOUND_IF
		if strings.HasSuffix(network, "6") {
			level, option = unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF
		}
		if err := unix.SetsockoptInt(int(fd), level, option, iface.Index); err != nil {
			return err
		}
	}
	for _, option := range config.CustomSockopt {
		if option.System != "" && option.System != runtime.GOOS || !strings.HasPrefix(network, option.Network) {
			continue
		}
		key, err := strconv.Atoi(option.Opt)
		if err != nil {
			return fmt.Errorf("custom socket option: %w", err)
		}
		level := syscall.IPPROTO_TCP
		if option.Level != "" {
			level, err = strconv.Atoi(option.Level)
			if err != nil {
				return err
			}
		}
		switch option.Type {
		case "int":
			value, err := strconv.Atoi(option.Value)
			if err != nil {
				return err
			}
			if err := syscall.SetsockoptInt(int(fd), level, key, value); err != nil {
				return err
			}
		case "str":
			if err := syscall.SetsockoptString(int(fd), level, key, option.Value); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown custom socket option type %q", option.Type)
		}
	}
	return nil
}
