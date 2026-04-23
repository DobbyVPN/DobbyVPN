package config

import (
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

const KeyLength = 32

type HString = Either[uint32, Pair[uint32, uint32]]
type HandshakeTime time.Duration
type Bytes uint64
type Key [KeyLength]byte

type Either[L, R any] struct {
	Left   L
	Right  R
	IsLeft bool
}

type Pair[L, R any] struct {
	First  L
	Second R
}

type Interface struct {
	PrivateKey Key
	Addresses  []IPCidr
	MTU        uint16
	DNS        []net.IP
	DNSSearch  []string

	JunkPacketCount            uint16  // Jc
	JunkPacketMinSize          uint16  // Jmin
	JunkPacketMaxSize          uint16  // Jmax
	InitPacketJunkSize         uint16  // S1
	ResponsePacketJunkSize     uint16  // S2
	CookieReplyPacketJunkSize  uint16  // S3
	TransportPacketJunkSize    uint16  // S4
	InitPacketMagicHeader      HString // H1
	ResponsePacketMagicHeader  HString // H2
	UnderloadPacketMagicHeader HString // H3
	TransportPacketMagicHeader HString // H4

	IPackets map[string]string
}

type Peer struct {
	PublicKey           Key
	PresharedKey        Key
	AllowedIPs          []IPCidr
	Endpoint            Endpoint
	PersistentKeepalive uint16

	RxBytes           Bytes
	TxBytes           Bytes
	LastHandshakeTime HandshakeTime
}

type IPCidr struct {
	IP   net.IP
	Cidr uint8
}

func (r *IPCidr) String() string {
	return fmt.Sprintf("%s/%d", r.IP.String(), r.Cidr)
}

func (r *IPCidr) Bits() uint8 {
	if r.IP.To4() != nil {
		return 32
	}
	return 128
}

func (r *IPCidr) IPNet() net.IPNet {
	return net.IPNet{
		IP:   r.IP,
		Mask: net.CIDRMask(int(r.Cidr), int(r.Bits())),
	}
}

func (r *IPCidr) MaskSelf() {
	bits := int(r.Bits())
	mask := net.CIDRMask(int(r.Cidr), bits)
	for i := 0; i < bits/8; i++ {
		r.IP[i] &= mask[i]
	}
}

func (k *Key) HexString() string {
	return hex.EncodeToString(k[:])
}

func (k *Key) IsZero() bool {
	var zeros Key
	return subtle.ConstantTimeCompare(zeros[:], k[:]) == 1
}

type Endpoint struct {
	Host string
	Port uint16
}

func (e *Endpoint) String() string {
	if strings.IndexByte(e.Host, ':') > 0 {
		return fmt.Sprintf("[%s]:%d", e.Host, e.Port)
	}
	return fmt.Sprintf("%s:%d", e.Host, e.Port)
}

func (e *Endpoint) IsEmpty() bool {
	return len(e.Host) == 0
}
