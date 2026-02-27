package direct

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// ───────────── IPv4 Header ─────────────

const ipv4MinLen = 20

type IPv4Header struct {
	Version    uint8
	IHL        uint8 // header length in 32-bit words
	TotalLen   uint16
	ID         uint16
	Flags      uint8
	FragOffset uint16
	TTL        uint8
	Protocol   uint8
	Checksum   uint16
	SrcIP      net.IP
	DstIP      net.IP
}

const (
	ProtoTCP uint8 = 6
	ProtoUDP uint8 = 17
)

func ParseIPv4(pkt []byte) (*IPv4Header, []byte, error) {
	if len(pkt) < ipv4MinLen {
		return nil, nil, errors.New("packet too short for IPv4")
	}

	ver := pkt[0] >> 4
	if ver != 4 {
		return nil, nil, fmt.Errorf("not IPv4: version=%d", ver)
	}

	ihl := pkt[0] & 0x0f
	hdrLen := int(ihl) * 4
	if hdrLen < ipv4MinLen || hdrLen > len(pkt) {
		return nil, nil, fmt.Errorf("invalid IHL: %d", ihl)
	}

	totalLen := binary.BigEndian.Uint16(pkt[2:4])
	if int(totalLen) > len(pkt) {
		totalLen = uint16(len(pkt))
	}

	h := &IPv4Header{
		Version:    ver,
		IHL:        ihl,
		TotalLen:   totalLen,
		ID:         binary.BigEndian.Uint16(pkt[4:6]),
		Flags:      pkt[6] >> 5,
		FragOffset: binary.BigEndian.Uint16(pkt[6:8]) & 0x1FFF,
		TTL:        pkt[8],
		Protocol:   pkt[9],
		Checksum:   binary.BigEndian.Uint16(pkt[10:12]),
		SrcIP:      net.IP(append([]byte(nil), pkt[12:16]...)),
		DstIP:      net.IP(append([]byte(nil), pkt[16:20]...)),
	}

	payload := pkt[hdrLen:totalLen]
	return h, payload, nil
}

// MarshalIPv4 builds a raw IPv4 header (20 bytes, no options).
// Checksum is computed automatically.
func MarshalIPv4(h *IPv4Header, payloadLen int) []byte {
	buf := make([]byte, ipv4MinLen)
	buf[0] = (4 << 4) | 5 // version=4, IHL=5
	totalLen := ipv4MinLen + payloadLen
	binary.BigEndian.PutUint16(buf[2:4], uint16(totalLen))
	binary.BigEndian.PutUint16(buf[4:6], h.ID)
	flagFrag := (uint16(h.Flags) << 13) | (h.FragOffset & 0x1FFF)
	binary.BigEndian.PutUint16(buf[6:8], flagFrag)
	buf[8] = h.TTL
	buf[9] = h.Protocol
	// checksum calculated below
	copy(buf[12:16], h.SrcIP.To4())
	copy(buf[16:20], h.DstIP.To4())

	// IP checksum
	binary.BigEndian.PutUint16(buf[10:12], 0)
	binary.BigEndian.PutUint16(buf[10:12], ipChecksum(buf))
	return buf
}

// ───────────── TCP Header ─────────────

const tcpMinLen = 20

const (
	TCPFlagFIN uint8 = 0x01
	TCPFlagSYN uint8 = 0x02
	TCPFlagRST uint8 = 0x04
	TCPFlagPSH uint8 = 0x08
	TCPFlagACK uint8 = 0x10
)

type TCPHeader struct {
	SrcPort    uint16
	DstPort    uint16
	SeqNum     uint32
	AckNum     uint32
	DataOffset uint8 // in 32-bit words
	Flags      uint8
	Window     uint16
	Checksum   uint16
	UrgPtr     uint16
	Options    []byte
}

func ParseTCP(data []byte) (*TCPHeader, []byte, error) {
	if len(data) < tcpMinLen {
		return nil, nil, errors.New("data too short for TCP header")
	}

	h := &TCPHeader{
		SrcPort:    binary.BigEndian.Uint16(data[0:2]),
		DstPort:    binary.BigEndian.Uint16(data[2:4]),
		SeqNum:     binary.BigEndian.Uint32(data[4:8]),
		AckNum:     binary.BigEndian.Uint32(data[8:12]),
		DataOffset: data[12] >> 4,
		Flags:      data[13] & 0x3F,
		Window:     binary.BigEndian.Uint16(data[14:16]),
		Checksum:   binary.BigEndian.Uint16(data[16:18]),
		UrgPtr:     binary.BigEndian.Uint16(data[18:20]),
	}

	hdrLen := int(h.DataOffset) * 4
	if hdrLen < tcpMinLen {
		hdrLen = tcpMinLen
	}
	if hdrLen > len(data) {
		hdrLen = len(data)
	}
	if hdrLen > tcpMinLen {
		h.Options = append([]byte(nil), data[tcpMinLen:hdrLen]...)
	}

	var payload []byte
	if hdrLen < len(data) {
		payload = data[hdrLen:]
	}
	return h, payload, nil
}

// MarshalTCP serializes the TCP header (without checksum — set it separately).
func MarshalTCP(h *TCPHeader, payload []byte) []byte {
	optLen := len(h.Options)
	// padding to 4-byte boundary
	padded := (tcpMinLen + optLen + 3) & ^3
	buf := make([]byte, padded+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], h.SrcPort)
	binary.BigEndian.PutUint16(buf[2:4], h.DstPort)
	binary.BigEndian.PutUint32(buf[4:8], h.SeqNum)
	binary.BigEndian.PutUint32(buf[8:12], h.AckNum)
	doff := uint8(padded / 4)
	buf[12] = doff << 4
	buf[13] = h.Flags
	binary.BigEndian.PutUint16(buf[14:16], h.Window)
	// checksum = 0 placeholder
	binary.BigEndian.PutUint16(buf[18:20], h.UrgPtr)
	if optLen > 0 {
		copy(buf[tcpMinLen:], h.Options)
	}
	copy(buf[padded:], payload)
	return buf
}

// ───────────── UDP Header ─────────────

const udpHdrLen = 8

type UDPHeader struct {
	SrcPort  uint16
	DstPort  uint16
	Length   uint16
	Checksum uint16
}

func ParseUDP(data []byte) (*UDPHeader, []byte, error) {
	if len(data) < udpHdrLen {
		return nil, nil, errors.New("data too short for UDP header")
	}
	h := &UDPHeader{
		SrcPort:  binary.BigEndian.Uint16(data[0:2]),
		DstPort:  binary.BigEndian.Uint16(data[2:4]),
		Length:   binary.BigEndian.Uint16(data[4:6]),
		Checksum: binary.BigEndian.Uint16(data[6:8]),
	}
	payloadEnd := int(h.Length)
	if payloadEnd > len(data) {
		payloadEnd = len(data)
	}
	if payloadEnd < udpHdrLen {
		payloadEnd = udpHdrLen
	}
	payload := data[udpHdrLen:payloadEnd]
	return h, payload, nil
}

func MarshalUDP(h *UDPHeader, payload []byte) []byte {
	buf := make([]byte, udpHdrLen+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], h.SrcPort)
	binary.BigEndian.PutUint16(buf[2:4], h.DstPort)
	binary.BigEndian.PutUint16(buf[4:6], uint16(udpHdrLen+len(payload)))
	// checksum placeholder = 0
	copy(buf[udpHdrLen:], payload)
	return buf
}

// ───────────── Checksums ─────────────

func ipChecksum(header []byte) uint16 {
	var sum uint32
	n := len(header)
	for i := 0; i+1 < n; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(header[i : i+2]))
	}
	if n%2 != 0 {
		sum += uint32(header[n-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}

// tcpChecksumFull computes the TCP checksum over a pseudo-header + full TCP segment.
func tcpChecksumFull(srcIP, dstIP net.IP, tcpSegment []byte) uint16 {
	return transportChecksum(srcIP, dstIP, ProtoTCP, tcpSegment)
}

// udpChecksumFull computes the UDP checksum over a pseudo-header + full UDP datagram.
func udpChecksumFull(srcIP, dstIP net.IP, udpDatagram []byte) uint16 {
	return transportChecksum(srcIP, dstIP, ProtoUDP, udpDatagram)
}

func transportChecksum(srcIP, dstIP net.IP, proto uint8, data []byte) uint16 {
	src4 := srcIP.To4()
	dst4 := dstIP.To4()

	// Pseudo-header: srcIP(4) + dstIP(4) + zero(1) + proto(1) + length(2)
	pseudo := make([]byte, 12)
	copy(pseudo[0:4], src4)
	copy(pseudo[4:8], dst4)
	pseudo[9] = proto
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(data)))

	var sum uint32
	for i := 0; i+1 < len(pseudo); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(pseudo[i : i+2]))
	}
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if len(data)%2 != 0 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}
