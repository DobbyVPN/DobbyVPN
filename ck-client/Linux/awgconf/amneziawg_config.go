//go:build linux
// +build linux

package main

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"
)

const KeyLength = 32

type IPCidr struct {
	IP   net.IP
	Cidr uint8
}

type Endpoint struct {
	Host string
	Port uint16
}

type Key [KeyLength]byte
type HandshakeTime time.Duration
type Bytes uint64

type Config struct {
	Name      string
	Interface Interface
	Peers     []Peer
}

type Interface struct {
	PrivateKey Key
	FwMark     uint16
	Addresses  []IPCidr
	ListenPort uint16
	MTU        uint16
	DNS        []net.IP
	DNSSearch  []string
	PreUp      string
	PostUp     string
	PreDown    string
	PostDown   string
	TableOff   bool

	JunkPacketCount            uint16
	JunkPacketMinSize          uint16
	JunkPacketMaxSize          uint16
	InitPacketJunkSize         uint16
	ResponsePacketJunkSize     uint16
	InitPacketMagicHeader      uint32
	ResponsePacketMagicHeader  uint32
	UnderloadPacketMagicHeader uint32
	TransportPacketMagicHeader uint32
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

func (e *Endpoint) String() string {
	if strings.IndexByte(e.Host, ':') > 0 {
		return fmt.Sprintf("[%s]:%d", e.Host, e.Port)
	}
	return fmt.Sprintf("%s:%d", e.Host, e.Port)
}

func (e *Endpoint) IsEmpty() bool {
	return len(e.Host) == 0
}

func (k *Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

func (k *Key) HexString() string {
	return hex.EncodeToString(k[:])
}

func (k *Key) IsZero() bool {
	var zeros Key
	return subtle.ConstantTimeCompare(zeros[:], k[:]) == 1
}

func (t HandshakeTime) IsEmpty() bool {
	return t == HandshakeTime(0)
}

func (t HandshakeTime) String() string {
	u := time.Unix(0, 0).Add(time.Duration(t)).Unix()
	n := time.Now().Unix()
	if u == n {
		return fmt.Sprintf("Now")
	} else if u > n {
		return fmt.Sprintf("System clock wound backward!")
	}
	left := n - u
	years := left / (365 * 24 * 60 * 60)
	left = left % (365 * 24 * 60 * 60)
	days := left / (24 * 60 * 60)
	left = left % (24 * 60 * 60)
	hours := left / (60 * 60)
	left = left % (60 * 60)
	minutes := left / 60
	seconds := left % 60
	s := make([]string, 0, 5)
	if years > 0 {
		s = append(s, fmt.Sprintf("%d year(s)", years))
	}
	if days > 0 {
		s = append(s, fmt.Sprintf("%d day(s)", days))
	}
	if hours > 0 {
		s = append(s, fmt.Sprintf("%d hour(s)", hours))
	}
	if minutes > 0 {
		s = append(s, fmt.Sprintf("%d minute(s)", minutes))
	}
	if seconds > 0 {
		s = append(s, fmt.Sprintf("%d second(s)", seconds))
	}
	timestamp := strings.Join(s, ",")
	return fmt.Sprintf("%s ago", timestamp)
}

func (b Bytes) String() string {
	if b < 1024 {
		return fmt.Sprintf("%d\u00a0B", b)
	} else if b < 1024*1024 {
		return fmt.Sprintf("%.2f\u00a0KiB", float64(b)/1024)
	} else if b < 1024*1024*1024 {
		return fmt.Sprintf("%.2f\u00a0MiB", float64(b)/(1024*1024))
	} else if b < 1024*1024*1024*1024 {
		return fmt.Sprintf("%.2f\u00a0GiB", float64(b)/(1024*1024*1024))
	}
	return fmt.Sprintf("%.2f\u00a0TiB", float64(b)/(1024*1024*1024)/1024)
}
