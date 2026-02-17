//go:build ios

package internal

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

func DecodePacket(raw []byte) ([]byte, bool) {
	if len(raw) <= 4 {
		return nil, false
	}
	return raw[4:], true
}

func EncodePacket(packet []byte) ([]byte, bool) {
	if len(packet) == 0 {
		return nil, false
	}

	ver := packet[0] >> 4
	var af uint32

	switch ver {
	case 4:
		af = uint32(unix.AF_INET)
	case 6:
		af = uint32(unix.AF_INET6)
	default:
		return nil, false
	}

	out := make([]byte, len(packet)+4)
	binary.BigEndian.PutUint32(out[:4], af)
	copy(out[4:], packet)

	return out, true
}
