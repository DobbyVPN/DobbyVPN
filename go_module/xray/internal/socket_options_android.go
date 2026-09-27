//go:build android

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
	if err := applyPlatformOutboundSocketBinding(fd, config); err != nil {
		return err
	}
	if strings.HasPrefix(network, "tcp") {
		if err := applyPlatformTCPOutboundSocketOptions(fd, config); err != nil {
			return err
		}
	}
	if err := applyPlatformCustomOutboundSocketOptions(network, fd, config.CustomSockopt); err != nil {
		return err
	}
	return applyPlatformTproxySocketOption(fd, config)
}

func applyPlatformOutboundSocketBinding(fd uintptr, config *internet.SocketConfig) error {
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
	return nil
}

func applyPlatformTCPOutboundSocketOptions(fd uintptr, config *internet.SocketConfig) error {
	tfo := config.ParseTFOValue()
	if err := applyPlatformTCPFastOpenOption(fd, tfo); err != nil {
		return err
	}
	if err := applyPlatformOptionalTCPSocketStringOption(fd, config.TcpCongestion, syscall.SOL_TCP, syscall.TCP_CONGESTION, "TCP_CONGESTION"); err != nil {
		return err
	}
	if err := applyPlatformOptionalTCPSocketIntOption(fd, int(config.TcpWindowClamp), syscall.IPPROTO_TCP, syscall.TCP_WINDOW_CLAMP, "TCP_WINDOW_CLAMP"); err != nil {
		return err
	}
	if err := applyPlatformOptionalTCPSocketIntOption(fd, int(config.TcpUserTimeout), syscall.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, "TCP_USER_TIMEOUT"); err != nil {
		return err
	}
	return applyPlatformOptionalTCPSocketIntOption(fd, int(config.TcpMaxSeg), syscall.IPPROTO_TCP, unix.TCP_MAXSEG, "TCP_MAXSEG")
}

func applyPlatformTCPFastOpenOption(fd uintptr, tfo int) error {
	if tfo > 0 {
		tfo = 1
	}
	if tfo < 0 {
		return nil
	}
	return setPlatformTCPSocketIntOption(fd, syscall.SOL_TCP, unix.TCP_FASTOPEN_CONNECT, tfo, "TCP_FASTOPEN_CONNECT")
}

func applyPlatformOptionalTCPSocketIntOption(fd uintptr, value, level, option int, name string) error {
	if value <= 0 {
		return nil
	}
	return setPlatformTCPSocketIntOption(fd, level, option, value, name)
}

func applyPlatformOptionalTCPSocketStringOption(fd uintptr, value string, level, option int, name string) error {
	if value == "" {
		return nil
	}
	if err := syscall.SetsockoptString(int(fd), level, option, value); err != nil {
		return fmt.Errorf("failed to set %s: %w", name, err)
	}
	return nil
}

func setPlatformTCPSocketIntOption(fd uintptr, level, option, value int, name string) error {
	if err := syscall.SetsockoptInt(int(fd), level, option, value); err != nil {
		return fmt.Errorf("failed to set %s: %w", name, err)
	}
	return nil
}

func applyPlatformCustomOutboundSocketOptions(network string, fd uintptr, options []*internet.CustomSockopt) error {
	for _, custom := range options {
		if custom.System != "" && custom.System != runtime.GOOS {
			continue
		}
		if !strings.HasPrefix(network, custom.Network) {
			continue
		}
		if err := applyPlatformCustomSocketOption(fd, custom); err != nil {
			return err
		}
	}
	return nil
}

func applyPlatformCustomSocketOption(fd uintptr, custom *internet.CustomSockopt) error {
	if custom.Opt == "" {
		return fmt.Errorf("custom socket option has no opt")
	}
	option, _ := strconv.Atoi(custom.Opt)
	level := 0x6
	if custom.Level != "" {
		level, _ = strconv.Atoi(custom.Level)
	}
	if custom.Type == "int" {
		value, _ := strconv.Atoi(custom.Value)
		if err := syscall.SetsockoptInt(int(fd), level, option, value); err != nil {
			return fmt.Errorf("failed to set CustomSockoptInt: %w", err)
		}
		return nil
	}
	if custom.Type == "str" {
		if err := syscall.SetsockoptString(int(fd), level, option, custom.Value); err != nil {
			return fmt.Errorf("failed to set CustomSockoptString: %w", err)
		}
		return nil
	}
	return fmt.Errorf("unknown CustomSockopt type %q", custom.Type)
}

func applyPlatformTproxySocketOption(fd uintptr, config *internet.SocketConfig) error {
	if config.Tproxy.IsEnabled() {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_IP, syscall.IP_TRANSPARENT, 1); err != nil {
			return fmt.Errorf("failed to set IP_TRANSPARENT: %w", err)
		}
	}
	return nil
}
