package config

import (
	"fmt"
	"go_module/awg/config/fwmark"
	"strings"
)

func writeInterfaceJunkSettings(output *strings.Builder, iface *Interface) {
	if iface.JunkPacketCount > 0 {
		fmt.Fprintf(output, "jc=%d\n", iface.JunkPacketCount)
	}
	if iface.JunkPacketMinSize > 0 {
		fmt.Fprintf(output, "jmin=%d\n", iface.JunkPacketMinSize)
	}
	if iface.JunkPacketMaxSize > 0 {
		fmt.Fprintf(output, "jmax=%d\n", iface.JunkPacketMaxSize)
	}
	if iface.InitPacketJunkSize > 0 {
		fmt.Fprintf(output, "s1=%d\n", iface.InitPacketJunkSize)
	}
	if iface.ResponsePacketJunkSize > 0 {
		fmt.Fprintf(output, "s2=%d\n", iface.ResponsePacketJunkSize)
	}
	if iface.CookieReplyPacketJunkSize > 0 {
		fmt.Fprintf(output, "s3=%d\n", iface.CookieReplyPacketJunkSize)
	}
	if iface.TransportPacketJunkSize > 0 {
		fmt.Fprintf(output, "s4=%d\n", iface.TransportPacketJunkSize)
	}
}

func writeMagicHeader(output *strings.Builder, header HString, name string) {
	if header.IsLeft && header.Left > 0 {
		fmt.Fprintf(output, "%s=%d\n", name, header.Left)
		return
	}
	if !header.IsLeft && header.Right.First > 0 && header.Right.Second > header.Right.First {
		fmt.Fprintf(output, "%s=%d-%d\n", name, header.Right.First, header.Right.Second)
	}
}

func writeInterfaceMagicHeaders(output *strings.Builder, iface *Interface) {
	writeMagicHeader(output, iface.InitPacketMagicHeader, "h1")
	writeMagicHeader(output, iface.ResponsePacketMagicHeader, "h2")
	writeMagicHeader(output, iface.UnderloadPacketMagicHeader, "h3")
	writeMagicHeader(output, iface.TransportPacketMagicHeader, "h4")
}

func writePeer(output *strings.Builder, peer *Peer) {
	fmt.Fprintf(output, "public_key=%s\n", peer.PublicKey.HexString())

	if !peer.PresharedKey.IsZero() {
		fmt.Fprintf(output, "preshared_key=%s\n", peer.PresharedKey.HexString())
	}

	if !peer.Endpoint.IsEmpty() {
		resolvedIP := peer.Endpoint.Host // FIXME: add platdform dependent Enpoint host recognition
		resolvedEndpoint := Endpoint{resolvedIP, peer.Endpoint.Port}
		fmt.Fprintf(output, "endpoint=%s\n", resolvedEndpoint.String())
	}

	fmt.Fprintf(output, "persistent_keepalive_interval=%d\n", peer.PersistentKeepalive)

	if len(peer.AllowedIPs) > 0 {
		fmt.Fprintf(output, "replace_allowed_ips=true\n")
		for _, address := range peer.AllowedIPs {
			fmt.Fprintf(output, "allowed_ip=%s\n", address.String())
		}
	}
}

func (conf *Config) ToUAPI() (uapi string, dnsErr error) {
	var output strings.Builder
	fmt.Fprintf(&output, "private_key=%s\n", conf.Interface.PrivateKey.HexString())

	if fwmark.FirewallMarkRequired {
		fmt.Fprintf(&output, "fwmark=%d\n", 51820)
	}

	writeInterfaceJunkSettings(&output, &conf.Interface)
	writeInterfaceMagicHeaders(&output, &conf.Interface)

	for key, value := range conf.Interface.IPackets {
		fmt.Fprintf(&output, "%s=%s\n", key, value)
	}

	if len(conf.Peers) > 0 {
		fmt.Fprintf(&output, "replace_peers=true\n")
	}

	for i := range conf.Peers {
		writePeer(&output, &conf.Peers[i])
	}
	return output.String(), nil
}
