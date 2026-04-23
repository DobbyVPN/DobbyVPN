package config

import (
	"fmt"
	"go_module/awg/config/fwmark"
	"strings"
)

func (conf *Config) ToUAPI() (uapi string, dnsErr error) {
	var output strings.Builder
	output.WriteString(fmt.Sprintf("private_key=%s\n", conf.Interface.PrivateKey.HexString()))

	if fwmark.FirewallMarkRequired {
		output.WriteString(fmt.Sprintf("fwmark=%d\n", 51820))
	}

	if conf.Interface.JunkPacketCount > 0 {
		output.WriteString(fmt.Sprintf("jc=%d\n", conf.Interface.JunkPacketCount))
	}

	if conf.Interface.JunkPacketMinSize > 0 {
		output.WriteString(fmt.Sprintf("jmin=%d\n", conf.Interface.JunkPacketMinSize))
	}

	if conf.Interface.JunkPacketMaxSize > 0 {
		output.WriteString(fmt.Sprintf("jmax=%d\n", conf.Interface.JunkPacketMaxSize))
	}

	if conf.Interface.InitPacketJunkSize > 0 {
		output.WriteString(fmt.Sprintf("s1=%d\n", conf.Interface.InitPacketJunkSize))
	}

	if conf.Interface.ResponsePacketJunkSize > 0 {
		output.WriteString(fmt.Sprintf("s2=%d\n", conf.Interface.ResponsePacketJunkSize))
	}

	if conf.Interface.CookieReplyPacketJunkSize > 0 {
		output.WriteString(fmt.Sprintf("s3=%d\n", conf.Interface.CookieReplyPacketJunkSize))
	}

	if conf.Interface.TransportPacketJunkSize > 0 {
		output.WriteString(fmt.Sprintf("s4=%d\n", conf.Interface.TransportPacketJunkSize))
	}

	if conf.Interface.InitPacketMagicHeader.IsLeft && conf.Interface.InitPacketMagicHeader.Left > 0 {
		output.WriteString(fmt.Sprintf("h1=%d\n", conf.Interface.InitPacketMagicHeader.Left))
	}

	if conf.Interface.ResponsePacketMagicHeader.IsLeft && conf.Interface.ResponsePacketMagicHeader.Left > 0 {
		output.WriteString(fmt.Sprintf("h2=%d\n", conf.Interface.ResponsePacketMagicHeader.Left))
	}

	if conf.Interface.UnderloadPacketMagicHeader.IsLeft && conf.Interface.UnderloadPacketMagicHeader.Left > 0 {
		output.WriteString(fmt.Sprintf("h3=%d\n", conf.Interface.UnderloadPacketMagicHeader.Left))
	}

	if conf.Interface.TransportPacketMagicHeader.IsLeft && conf.Interface.TransportPacketMagicHeader.Left > 0 {
		output.WriteString(fmt.Sprintf("h4=%d\n", conf.Interface.TransportPacketMagicHeader.Left))
	}

	if !conf.Interface.InitPacketMagicHeader.IsLeft && conf.Interface.InitPacketMagicHeader.Right.First > 0 && conf.Interface.InitPacketMagicHeader.Right.Second > conf.Interface.InitPacketMagicHeader.Right.First {
		output.WriteString(fmt.Sprintf("h1=%d-%d\n", conf.Interface.InitPacketMagicHeader.Right.First, conf.Interface.InitPacketMagicHeader.Right.Second))
	}

	if !conf.Interface.ResponsePacketMagicHeader.IsLeft && conf.Interface.ResponsePacketMagicHeader.Right.First > 0 && conf.Interface.ResponsePacketMagicHeader.Right.Second > conf.Interface.ResponsePacketMagicHeader.Right.First {
		output.WriteString(fmt.Sprintf("h2=%d-%d\n", conf.Interface.ResponsePacketMagicHeader.Right.First, conf.Interface.ResponsePacketMagicHeader.Right.Second))
	}

	if !conf.Interface.UnderloadPacketMagicHeader.IsLeft && conf.Interface.UnderloadPacketMagicHeader.Right.First > 0 && conf.Interface.UnderloadPacketMagicHeader.Right.Second > conf.Interface.UnderloadPacketMagicHeader.Right.First {
		output.WriteString(fmt.Sprintf("h3=%d-%d\n", conf.Interface.UnderloadPacketMagicHeader.Right.First, conf.Interface.UnderloadPacketMagicHeader.Right.Second))
	}

	if !conf.Interface.TransportPacketMagicHeader.IsLeft && conf.Interface.TransportPacketMagicHeader.Right.First > 0 && conf.Interface.TransportPacketMagicHeader.Right.Second > conf.Interface.TransportPacketMagicHeader.Right.First {
		output.WriteString(fmt.Sprintf("h4=%d-%d\n", conf.Interface.TransportPacketMagicHeader.Right.First, conf.Interface.TransportPacketMagicHeader.Right.Second))
	}

	for key, value := range conf.Interface.IPackets {
		output.WriteString(fmt.Sprintf("%s=%s\n", key, value))
	}

	if len(conf.Peers) > 0 {
		output.WriteString("replace_peers=true\n")
	}

	for _, peer := range conf.Peers {
		output.WriteString(fmt.Sprintf("public_key=%s\n", peer.PublicKey.HexString()))

		if !peer.PresharedKey.IsZero() {
			output.WriteString(fmt.Sprintf("preshared_key=%s\n", peer.PresharedKey.HexString()))
		}

		if !peer.Endpoint.IsEmpty() {
			resolvedIP := peer.Endpoint.Host // FIXME: add platdform dependent Enpoint host recognition
			resolvedEndpoint := Endpoint{resolvedIP, peer.Endpoint.Port}
			output.WriteString(fmt.Sprintf("endpoint=%s\n", resolvedEndpoint.String()))
		}

		output.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", peer.PersistentKeepalive))

		if len(peer.AllowedIPs) > 0 {
			output.WriteString("replace_allowed_ips=true\n")
			for _, address := range peer.AllowedIPs {
				output.WriteString(fmt.Sprintf("allowed_ip=%s\n", address.String()))
			}
		}
	}
	return output.String(), nil
}
