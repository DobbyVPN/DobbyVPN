package internal

import (
	"encoding/binary"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/xtls/xray-core/transport/internet"
)

func applyPlatformOutboundSocketOptions(network, _ string, fd uintptr, config *internet.SocketConfig) error {
	if config == nil {
		return nil
	}
	socket := syscall.Handle(fd)
	if strings.HasPrefix(network, "tcp") {
		tfo := config.ParseTFOValue()
		if tfo > 0 {
			tfo = 1
		}
		if tfo >= 0 {
			if err := syscall.SetsockoptInt(socket, syscall.IPPROTO_TCP, 15, tfo); err != nil {
				return err
			}
		}
	}
	if config.Interface != "" {
		iface, err := net.InterfaceByName(config.Interface)
		if err != nil {
			return err
		}
		level, index := syscall.IPPROTO_IPV6, iface.Index
		if !strings.HasSuffix(network, "6") {
			level = syscall.IPPROTO_IP
			var value [4]byte
			binary.BigEndian.PutUint32(value[:], uint32(index))
			index = int(binary.NativeEndian.Uint32(value[:]))
		}
		if err := syscall.SetsockoptInt(socket, level, 31, index); err != nil {
			return err
		}
	}
	for _, option := range config.CustomSockopt {
		if option.System != "" && option.System != runtime.GOOS || !strings.HasPrefix(network, option.Network) {
			continue
		}
		key, err := strconv.Atoi(option.Opt)
		if err != nil {
			return err
		}
		level := syscall.IPPROTO_TCP
		if option.Level != "" {
			level, err = strconv.Atoi(option.Level)
			if err != nil {
				return err
			}
		}
		if option.Type != "int" {
			return fmt.Errorf("unsupported Windows socket option type %q", option.Type)
		}
		value, err := strconv.Atoi(option.Value)
		if err != nil {
			return err
		}
		if err := syscall.SetsockoptInt(socket, level, key, value); err != nil {
			return err
		}
	}
	return nil
}
