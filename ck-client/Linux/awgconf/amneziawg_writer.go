//go:build linux
// +build linux

package main

import (
	"fmt"
	"strings"
)

func (conf *Config) ToUAPI() (uapi string, dnsErr error) {
	var output strings.Builder
	output.WriteString(fmt.Sprintf("private_key=%s\n", conf.Interface.PrivateKey.HexString()))
	output.WriteString(fmt.Sprintf("fwmark=%d\n", conf.Interface.FwMark))

	if conf.Interface.ListenPort > 0 {
		output.WriteString(fmt.Sprintf("listen_port=%d\n", conf.Interface.ListenPort))
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

	if conf.Interface.InitPacketMagicHeader > 0 {
		output.WriteString(fmt.Sprintf("h1=%d\n", conf.Interface.InitPacketMagicHeader))
	}

	if conf.Interface.ResponsePacketMagicHeader > 0 {
		output.WriteString(fmt.Sprintf("h2=%d\n", conf.Interface.ResponsePacketMagicHeader))
	}

	if conf.Interface.UnderloadPacketMagicHeader > 0 {
		output.WriteString(fmt.Sprintf("h3=%d\n", conf.Interface.UnderloadPacketMagicHeader))
	}

	if conf.Interface.TransportPacketMagicHeader > 0 {
		output.WriteString(fmt.Sprintf("h4=%d\n", conf.Interface.TransportPacketMagicHeader))
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
			output.WriteString(fmt.Sprintf("endpoint=%s\n", peer.Endpoint.String()))
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
