package config

import (
	"fmt"
	"go_module/awg/config/fwmark"
	"strings"
)

func (conf *Config) toUAPIFwMark(output *strings.Builder) {
	if fwmark.FirewallMarkRequired {
		fmt.Fprintf(output, "fwmark=%d\n", 51820)
	}
}

func (conf *Config) toUAPIJunk(output *strings.Builder) {
	if conf.Interface.JunkPacketCount > 0 {
		fmt.Fprintf(output, "jc=%d\n", conf.Interface.JunkPacketCount)
	}

	if conf.Interface.JunkPacketMinSize > 0 {
		fmt.Fprintf(output, "jmin=%d\n", conf.Interface.JunkPacketMinSize)
	}

	if conf.Interface.JunkPacketMaxSize > 0 {
		fmt.Fprintf(output, "jmax=%d\n", conf.Interface.JunkPacketMaxSize)
	}

	if conf.Interface.InitPacketJunkSize > 0 {
		fmt.Fprintf(output, "s1=%d\n", conf.Interface.InitPacketJunkSize)
	}

	if conf.Interface.ResponsePacketJunkSize > 0 {
		fmt.Fprintf(output, "s2=%d\n", conf.Interface.ResponsePacketJunkSize)
	}

	if conf.Interface.CookieReplyPacketJunkSize > 0 {
		fmt.Fprintf(output, "s3=%d\n", conf.Interface.CookieReplyPacketJunkSize)
	}

	if conf.Interface.TransportPacketJunkSize > 0 {
		fmt.Fprintf(output, "s4=%d\n", conf.Interface.TransportPacketJunkSize)
	}
}

func (conf *Config) toUAPIHeaders(output *strings.Builder) {
	if conf.Interface.InitPacketMagicHeader.IsLeft && conf.Interface.InitPacketMagicHeader.Left > 0 {
		fmt.Fprintf(output, "h1=%d\n", conf.Interface.InitPacketMagicHeader.Left)
	}

	if conf.Interface.ResponsePacketMagicHeader.IsLeft && conf.Interface.ResponsePacketMagicHeader.Left > 0 {
		fmt.Fprintf(output, "h2=%d\n", conf.Interface.ResponsePacketMagicHeader.Left)
	}

	if conf.Interface.UnderloadPacketMagicHeader.IsLeft && conf.Interface.UnderloadPacketMagicHeader.Left > 0 {
		fmt.Fprintf(output, "h3=%d\n", conf.Interface.UnderloadPacketMagicHeader.Left)
	}

	if conf.Interface.TransportPacketMagicHeader.IsLeft && conf.Interface.TransportPacketMagicHeader.Left > 0 {
		fmt.Fprintf(output, "h4=%d\n", conf.Interface.TransportPacketMagicHeader.Left)
	}
}

func (conf *Config) toUAPIHeadersRanges(output *strings.Builder) {
	if !conf.Interface.InitPacketMagicHeader.IsLeft && conf.Interface.InitPacketMagicHeader.Right.First > 0 && conf.Interface.InitPacketMagicHeader.Right.Second > conf.Interface.InitPacketMagicHeader.Right.First {
		fmt.Fprintf(output, "h1=%d-%d\n", conf.Interface.InitPacketMagicHeader.Right.First, conf.Interface.InitPacketMagicHeader.Right.Second)
	}

	if !conf.Interface.ResponsePacketMagicHeader.IsLeft && conf.Interface.ResponsePacketMagicHeader.Right.First > 0 && conf.Interface.ResponsePacketMagicHeader.Right.Second > conf.Interface.ResponsePacketMagicHeader.Right.First {
		fmt.Fprintf(output, "h2=%d-%d\n", conf.Interface.ResponsePacketMagicHeader.Right.First, conf.Interface.ResponsePacketMagicHeader.Right.Second)
	}

	if !conf.Interface.UnderloadPacketMagicHeader.IsLeft && conf.Interface.UnderloadPacketMagicHeader.Right.First > 0 && conf.Interface.UnderloadPacketMagicHeader.Right.Second > conf.Interface.UnderloadPacketMagicHeader.Right.First {
		fmt.Fprintf(output, "h3=%d-%d\n", conf.Interface.UnderloadPacketMagicHeader.Right.First, conf.Interface.UnderloadPacketMagicHeader.Right.Second)
	}

	if !conf.Interface.TransportPacketMagicHeader.IsLeft && conf.Interface.TransportPacketMagicHeader.Right.First > 0 && conf.Interface.TransportPacketMagicHeader.Right.Second > conf.Interface.TransportPacketMagicHeader.Right.First {
		fmt.Fprintf(output, "h4=%d-%d\n", conf.Interface.TransportPacketMagicHeader.Right.First, conf.Interface.TransportPacketMagicHeader.Right.Second)
	}
}

func (conf *Config) toUAPIIPackets(output *strings.Builder) {
	for key, value := range conf.Interface.IPackets {
		fmt.Fprintf(output, "%s=%s\n", key, value)
	}
}

func (conf *Config) toUAPIPeer(output *strings.Builder, peer Peer) {
	fmt.Fprintf(output, "public_key=%s\n", peer.PublicKey.HexString())

	if !peer.PresharedKey.IsZero() {
		fmt.Fprintf(output, "preshared_key=%s\n", peer.PresharedKey.HexString())
	}

	if !peer.Endpoint.IsEmpty() {
		// FIXME: add platdform dependent Enpoint host recognition
		resolvedIP := peer.Endpoint.Host
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

	conf.toUAPIFwMark(&output)
	conf.toUAPIJunk(&output)
	conf.toUAPIHeaders(&output)
	conf.toUAPIHeadersRanges(&output)
	conf.toUAPIIPackets(&output)

	if len(conf.Peers) > 0 {
		fmt.Fprintf(&output, "replace_peers=true\n")
	}

	for _, peer := range conf.Peers {
		conf.toUAPIPeer(&output, peer)
	}
	return output.String(), nil
}
