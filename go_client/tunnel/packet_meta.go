package tunnel

import (
	"errors"
	"net/netip"

	"go_client/georouting"
)

var (
	ErrPacketTooShort       = errors.New("packet too short")
	ErrUnsupportedIPVersion = errors.New("unsupported ip version")
)

type tcpFlags struct {
	fin bool
	rst bool
	syn bool
}

func parsePacketMetadata(pkt []byte) (georouting.Metadata, tcpFlags, error) {
	if len(pkt) < 1 {
		return georouting.Metadata{}, tcpFlags{}, ErrPacketTooShort
	}
	ver := pkt[0] >> 4
	switch ver {
	case 4:
		return parseIPv4(pkt)
	case 6:
		return parseIPv6(pkt)
	default:
		return georouting.Metadata{}, tcpFlags{}, ErrUnsupportedIPVersion
	}
}

func parseIPv4(pkt []byte) (georouting.Metadata, tcpFlags, error) {
	if len(pkt) < 20 {
		return georouting.Metadata{}, tcpFlags{}, ErrPacketTooShort
	}
	ihl := int(pkt[0]&0x0F) * 4
	if ihl < 20 || len(pkt) < ihl {
		return georouting.Metadata{}, tcpFlags{}, ErrPacketTooShort
	}
	proto := pkt[9]

	src := netip.AddrFrom4([4]byte{pkt[12], pkt[13], pkt[14], pkt[15]})
	dst := netip.AddrFrom4([4]byte{pkt[16], pkt[17], pkt[18], pkt[19]})

	meta := georouting.Metadata{
		SrcIP: src,
		DstIP: dst,
	}

	l4 := ihl
	var flags tcpFlags

	switch proto {
	case 6: // TCP
		meta.Network = georouting.NetworkTCP
		if len(pkt) >= l4+4 {
			meta.SrcPort = uint16(pkt[l4])<<8 | uint16(pkt[l4+1])
			meta.DstPort = uint16(pkt[l4+2])<<8 | uint16(pkt[l4+3])
		}
		if len(pkt) >= l4+14 {
			f := pkt[l4+13]
			flags.fin = f&0x01 != 0
			flags.syn = f&0x02 != 0
			flags.rst = f&0x04 != 0
		}

	case 17: // UDP
		meta.Network = georouting.NetworkUDP
		if len(pkt) >= l4+4 {
			meta.SrcPort = uint16(pkt[l4])<<8 | uint16(pkt[l4+1])
			meta.DstPort = uint16(pkt[l4+2])<<8 | uint16(pkt[l4+3])
		}

	case 1: // ICMP
		meta.Network = georouting.NetworkICMP

	default:
		meta.Network = georouting.NetworkAny
	}
	return meta, flags, nil
}

func parseIPv6(pkt []byte) (georouting.Metadata, tcpFlags, error) {
	if len(pkt) < 40 {
		return georouting.Metadata{}, tcpFlags{}, ErrPacketTooShort
	}
	next := pkt[6]

	var src16 [16]byte
	var dst16 [16]byte
	copy(src16[:], pkt[8:24])
	copy(dst16[:], pkt[24:40])

	meta := georouting.Metadata{
		SrcIP: netip.AddrFrom16(src16),
		DstIP: netip.AddrFrom16(dst16),
	}

	off, nh := skipIPv6Extensions(pkt, 40, next)
	next = nh

	var flags tcpFlags
	switch next {
	case 6: // TCP
		meta.Network = georouting.NetworkTCP
		if len(pkt) >= off+4 {
			meta.SrcPort = uint16(pkt[off])<<8 | uint16(pkt[off+1])
			meta.DstPort = uint16(pkt[off+2])<<8 | uint16(pkt[off+3])
		}
		if len(pkt) >= off+14 {
			f := pkt[off+13]
			flags.fin = f&0x01 != 0
			flags.syn = f&0x02 != 0
			flags.rst = f&0x04 != 0
		}

	case 17: // UDP
		meta.Network = georouting.NetworkUDP
		if len(pkt) >= off+4 {
			meta.SrcPort = uint16(pkt[off])<<8 | uint16(pkt[off+1])
			meta.DstPort = uint16(pkt[off+2])<<8 | uint16(pkt[off+3])
		}

	case 58: // ICMPv6
		meta.Network = georouting.NetworkICMP

	default:
		meta.Network = georouting.NetworkAny
	}

	return meta, flags, nil
}

// Минимальный разбор extension headers IPv6.
// Если не получилось — возвращаем исходный offset 40 и next header, чтобы хотя бы IP можно было использовать для роутинга.
func skipIPv6Extensions(pkt []byte, off int, next uint8) (int, uint8) {
	for {
		switch next {
		// Hop-by-Hop Options (0), Routing (43), Destination Options (60)
		case 0, 43, 60:
			if len(pkt) < off+2 {
				return 40, pkt[6]
			}
			nxt := pkt[off]
			hdrExtLen := int(pkt[off+1])
			// (Hdr Ext Len + 1) * 8
			off += (hdrExtLen + 1) * 8
			next = nxt
			if len(pkt) < off {
				return 40, pkt[6]
			}
			continue

		// Fragment (44) — фикс 8 байт
		case 44:
			if len(pkt) < off+8 {
				return 40, pkt[6]
			}
			nxt := pkt[off]
			off += 8
			next = nxt
			continue

		// AH (51)
		case 51:
			if len(pkt) < off+2 {
				return 40, pkt[6]
			}
			nxt := pkt[off]
			pl := int(pkt[off+1])
			// (Payload Len + 2) * 4
			off += (pl + 2) * 4
			next = nxt
			if len(pkt) < off {
				return 40, pkt[6]
			}
			continue

		default:
			return off, next
		}
	}
}
