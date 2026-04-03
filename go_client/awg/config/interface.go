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
