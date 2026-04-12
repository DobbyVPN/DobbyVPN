package config

import "net"

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

type HString = Either[uint32, Pair[uint32, uint32]]

type Either[L, R any] struct {
	Left   L
	Right  R
	IsLeft bool
}

type Pair[L, R any] struct {
	First  L
	Second R
}
