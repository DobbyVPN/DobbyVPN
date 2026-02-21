package internal

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
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
	CookieReplyPacketJunkSize  uint16
	TransportPacketJunkSize    uint16
	InitPacketMagicHeader      uint32
	ResponsePacketMagicHeader  uint32
	UnderloadPacketMagicHeader uint32
	TransportPacketMagicHeader uint32

	IPackets map[string]string
	JPackets map[string]string
	ITime    uint32
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

func (conf *Config) IntersectsWith(other *Config) bool {
	type hashableIPCidr struct {
		ip   string
		cidr byte
	}
	allRoutes := make(map[hashableIPCidr]bool, len(conf.Interface.Addresses)*2+len(conf.Peers)*3)
	for _, a := range conf.Interface.Addresses {
		allRoutes[hashableIPCidr{string(a.IP), byte(len(a.IP) * 8)}] = true
		a.MaskSelf()
		allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] = true
	}
	for i := range conf.Peers {
		for _, a := range conf.Peers[i].AllowedIPs {
			a.MaskSelf()
			allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] = true
		}
	}
	for _, a := range other.Interface.Addresses {
		if allRoutes[hashableIPCidr{string(a.IP), byte(len(a.IP) * 8)}] {
			return true
		}
		a.MaskSelf()
		if allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] {
			return true
		}
	}
	for i := range other.Peers {
		for _, a := range other.Peers[i].AllowedIPs {
			a.MaskSelf()
			if allRoutes[hashableIPCidr{string(a.IP), a.Cidr}] {
				return true
			}
		}
	}
	return false
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

func (k *Key) Public() *Key {
	var p [KeyLength]byte
	curve25519.ScalarBaseMult(&p, (*[KeyLength]byte)(k))
	return (*Key)(&p)
}

func NewPresharedKey() (*Key, error) {
	var k [KeyLength]byte
	_, err := rand.Read(k[:])
	if err != nil {
		return nil, err
	}
	return (*Key)(&k), nil
}

func NewPrivateKey() (*Key, error) {
	k, err := NewPresharedKey()
	if err != nil {
		return nil, err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k, nil
}

func NewPrivateKeyFromString(b64 string) (*Key, error) {
	return parseKeyBase64(b64)
}
