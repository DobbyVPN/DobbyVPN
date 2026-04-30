package config

import (
	"encoding/base64"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/unicode"
)

var _specialHandshakeTags = map[string]struct{}{
	"i1":    {},
	"i2":    {},
	"i3":    {},
	"i4":    {},
	"i5":    {},
	"j1":    {},
	"j2":    {},
	"j3":    {},
	"itime": {},
}

type ParseError struct {
	why      string
	offender string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s: %q", e.why, e.offender)
}

func parseIPCidr(s string) (*IPCidr, error) {
	var addrStr, cidrStr string

	i := strings.IndexByte(s, '/')
	if i < 0 {
		addrStr = s
	} else {
		addrStr, cidrStr = s[:i], s[i+1:]
	}

	addr := net.ParseIP(addrStr)
	if addr == nil {
		return nil, fmt.Errorf("invalid IP address")
	}
	maybeV4 := addr.To4()
	if maybeV4 != nil {
		addr = maybeV4
	}
	if cidrStr != "" {
		cidr, err := strconv.Atoi(cidrStr)
		if err != nil || cidr < 0 || cidr > 128 {
			return nil, err
		}
		if cidr > 32 && maybeV4 != nil {
			return nil, fmt.Errorf("invalid network prefix length")
		}
		return &IPCidr{addr, uint8(cidr)}, nil
	}
	var cidr uint8
	if maybeV4 != nil {
		cidr = 32
	} else {
		cidr = 128
	}
	return &IPCidr{addr, cidr}, nil
}

func parseEndpoint(s string) (*Endpoint, error) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return nil, fmt.Errorf("missing port from endpoint")
	}
	host, portStr := s[:i], s[i+1:]
	if host == "" {
		return nil, fmt.Errorf("invalid endpoint host")
	}
	port, err := parsePort(portStr)
	if err != nil {
		return nil, err
	}
	hostColon := strings.IndexByte(host, ':')
	if host[0] == '[' || host[len(host)-1] == ']' || hostColon > 0 {
		err := fmt.Errorf("brackets must contain an IPv6 address")
		if len(host) > 3 && host[0] == '[' && host[len(host)-1] == ']' && hostColon > 0 {
			end := len(host) - 1
			if i := strings.LastIndexByte(host, '%'); i > 1 {
				end = i
			}
			maybeV6 := net.ParseIP(host[1:end])
			if maybeV6 == nil || len(maybeV6) != net.IPv6len {
				return nil, err
			}
		} else {
			return nil, err
		}
		host = host[1 : len(host)-1]
	}
	return &Endpoint{host, port}, nil
}

func parseMTU(s string) (uint16, error) {
	m, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if m < 576 || m > 65535 {
		return 0, fmt.Errorf("invalid MTU")
	}
	return uint16(m), nil
}

func parsePort(s string) (uint16, error) {
	m, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if m < 0 || m > 65535 {
		return 0, fmt.Errorf("invalid port")
	}
	return uint16(m), nil
}

func parseUint16(value, name string) (uint16, error) {
	m, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if m < 0 || m > math.MaxUint16 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return uint16(m), nil
}

func parseHString(value, name string) (HString, error) {
	splitResult := strings.Split(value, "-")

	if len(splitResult) == 1 {
		m, err := strconv.ParseUint(splitResult[0], 10, 32)
		if err != nil {
			return HString{}, err
		}
		return Either[uint32, Pair[uint32, uint32]]{
			Left:   uint32(m),
			IsLeft: true,
		}, nil
	}
	if len(splitResult) == 2 {
		minRange, err := strconv.ParseUint(splitResult[0], 10, 32)
		if err != nil {
			return HString{}, err
		}
		maxRange, err := strconv.ParseUint(splitResult[1], 10, 32)
		if err != nil {
			return HString{}, err
		}
		if maxRange <= minRange {
			return HString{}, fmt.Errorf("invalid %s", name)
		}
		return Either[uint32, Pair[uint32, uint32]]{
			Right: Pair[uint32, uint32]{
				First:  uint32(minRange),
				Second: uint32(maxRange),
			},
			IsLeft: false,
		}, nil
	}
	return HString{}, fmt.Errorf("invalid %s", name)
}

func parsePersistentKeepalive(s string) (uint16, error) {
	if s == "off" {
		return 0, nil
	}
	m, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if m < 0 || m > 65535 {
		return 0, fmt.Errorf("invalid persistent keepalive")
	}
	return uint16(m), nil
}

func parseKeyBase64(s string) (*Key, error) {
	k, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}
	if len(k) != KeyLength {
		return nil, fmt.Errorf("keys must decode to exactly 32 bytes")
	}
	var key Key
	copy(key[:], k)
	return &key, nil
}

func splitList(s string) ([]string, error) {
	var out []string
	for _, split := range strings.Split(s, ",") {
		trim := strings.TrimSpace(split)
		if trim == "" {
			return nil, fmt.Errorf("two commas in a row")
		}
		out = append(out, trim)
	}
	return out, nil
}

type parserState int

const (
	inInterfaceSection parserState = iota
	inPeerSection
	notInASection
)

func parseInterfaceField(conf *Config, key, val string) error {
	switch key {
	case "privatekey":
		k, err := parseKeyBase64(val)
		if err != nil {
			return err
		}
		conf.Interface.PrivateKey = *k
		return nil
	case "jc":
		junkPacketCount, err := parseUint16(val, "junkPacketCount")
		if err != nil {
			return err
		}
		conf.Interface.JunkPacketCount = junkPacketCount
		return nil
	case "jmin":
		junkPacketMinSize, err := parseUint16(val, "junkPacketMinSize")
		if err != nil {
			return err
		}
		conf.Interface.JunkPacketMinSize = junkPacketMinSize
		return nil
	case "jmax":
		junkPacketMaxSize, err := parseUint16(val, "junkPacketMaxSize")
		if err != nil {
			return err
		}
		conf.Interface.JunkPacketMaxSize = junkPacketMaxSize
		return nil
	case "s1":
		initPacketJunkSize, err := parseUint16(val, "initPacketJunkSize")
		if err != nil {
			return err
		}
		conf.Interface.InitPacketJunkSize = initPacketJunkSize
		return nil
	case "s2":
		responsePacketJunkSize, err := parseUint16(val, "responsePacketJunkSize")
		if err != nil {
			return err
		}
		conf.Interface.ResponsePacketJunkSize = responsePacketJunkSize
		return nil
	case "s3":
		cookieReplyJunkSize, err := parseUint16(val, "cookieReplyPacketJunkSize")
		if err != nil {
			return err
		}
		conf.Interface.CookieReplyPacketJunkSize = cookieReplyJunkSize
		return nil
	case "s4":
		transportJunkSize, err := parseUint16(val, "transportPacketJunkSize")
		if err != nil {
			return err
		}
		conf.Interface.TransportPacketJunkSize = transportJunkSize
		return nil
	case "h1":
		initPacketMagicHeader, err := parseHString(val, "initPacketMagicHeader")
		if err != nil {
			return err
		}
		conf.Interface.InitPacketMagicHeader = initPacketMagicHeader
		return nil
	case "h2":
		responsePacketMagicHeader, err := parseHString(val, "responsePacketMagicHeader")
		if err != nil {
			return err
		}
		conf.Interface.ResponsePacketMagicHeader = responsePacketMagicHeader
		return nil
	case "h3":
		underloadPacketMagicHeader, err := parseHString(val, "underloadPacketMagicHeader")
		if err != nil {
			return err
		}
		conf.Interface.UnderloadPacketMagicHeader = underloadPacketMagicHeader
		return nil
	case "h4":
		transportPacketMagicHeader, err := parseHString(val, "transportPacketMagicHeader")
		if err != nil {
			return err
		}
		conf.Interface.TransportPacketMagicHeader = transportPacketMagicHeader
		return nil
	case "i1", "i2", "i3", "i4", "i5":
		if val == "" {
			return nil
		}
		if conf.Interface.IPackets == nil {
			conf.Interface.IPackets = make(map[string]string)
		}
		conf.Interface.IPackets[key] = val
		return nil
	case "mtu":
		m, err := parseMTU(val)
		if err != nil {
			return err
		}
		conf.Interface.MTU = m
		return nil
	case "address":
		addresses, err := splitList(val)
		if err != nil {
			return err
		}
		for _, address := range addresses {
			a, err := parseIPCidr(address)
			if err != nil {
				return err
			}
			conf.Interface.Addresses = append(conf.Interface.Addresses, *a)
		}
		return nil
	case "dns":
		addresses, err := splitList(val)
		if err != nil {
			return err
		}
		for _, address := range addresses {
			a := net.ParseIP(address)
			if a == nil {
				conf.Interface.DNSSearch = append(conf.Interface.DNSSearch, address)
			} else {
				conf.Interface.DNS = append(conf.Interface.DNS, a)
			}
		}
		return nil
	default:
		return fmt.Errorf("invalid key for [Interface] section")
	}
}

func parsePeerField(peer *Peer, key, val string) error {
	switch key {
	case "publickey":
		k, err := parseKeyBase64(val)
		if err != nil {
			return err
		}
		peer.PublicKey = *k
		return nil
	case "presharedkey":
		k, err := parseKeyBase64(val)
		if err != nil {
			return err
		}
		peer.PresharedKey = *k
		return nil
	case "allowedips":
		addresses, err := splitList(val)
		if err != nil {
			return err
		}
		for _, address := range addresses {
			a, err := parseIPCidr(address)
			if err != nil {
				return err
			}
			peer.AllowedIPs = append(peer.AllowedIPs, *a)
		}
		return nil
	case "persistentkeepalive":
		p, err := parsePersistentKeepalive(val)
		if err != nil {
			return err
		}
		peer.PersistentKeepalive = p
		return nil
	case "endpoint":
		e, err := parseEndpoint(val)
		if err != nil {
			return err
		}
		peer.Endpoint = *e
		return nil
	default:
		return fmt.Errorf("invalid key for [Peer] section")
	}
}

func FromWgQuick(s string, name string) (*Config, error) {
	if !TunnelNameIsValid(name) {
		return nil, fmt.Errorf("tunnel name is not valid")
	}
	lines := strings.Split(s, "\n")
	parserState := notInASection
	conf := Config{Name: name}
	sawPrivateKey := false
	conf.Interface.MTU = 1420
	var peer *Peer
	for _, line := range lines {
		pound := strings.IndexByte(line, '#')
		if pound >= 0 {
			line = line[:pound]
		}
		line = strings.TrimSpace(line)
		lineLower := strings.ToLower(line)
		if line == "" {
			continue
		}
		if lineLower == "[interface]" {
			conf.MaybeAddPeer(peer)
			parserState = inInterfaceSection
			continue
		}
		if lineLower == "[peer]" {
			conf.MaybeAddPeer(peer)
			peer = &Peer{}
			parserState = inPeerSection
			continue
		}
		if parserState == notInASection {
			return nil, fmt.Errorf("line must occur in a section")
		}
		equals := strings.IndexByte(line, '=')
		if equals < 0 {
			return nil, fmt.Errorf("config key is missing an equals separator")
		}
		key, val := strings.TrimSpace(lineLower[:equals]), strings.TrimSpace(line[equals+1:])
		if _, ok := _specialHandshakeTags[key]; !ok && val == "" {
			return nil, fmt.Errorf("key must have a value")
		}
		if parserState == inInterfaceSection {
			if err := parseInterfaceField(&conf, key, val); err != nil {
				return nil, err
			}
			if key == "privatekey" {
				sawPrivateKey = true
			}
		} else if parserState == inPeerSection {
			if err := parsePeerField(peer, key, val); err != nil {
				return nil, err
			}
		}
	}
	conf.MaybeAddPeer(peer)

	if !sawPrivateKey {
		return nil, fmt.Errorf("an interface must have a private key [none specified]")
	}
	for _, p := range conf.Peers {
		if p.PublicKey.IsZero() {
			return nil, fmt.Errorf("all peers must have public keys [none specified]")
		}
	}

	return &conf, nil
}

func FromWgQuickWithUnknownEncoding(s, name string) (*Config, error) {
	c, firstErr := FromWgQuick(s, name)
	if firstErr == nil {
		return c, nil
	}
	for _, encoding := range unicode.All {
		decoded, err := encoding.NewDecoder().String(s)
		if err == nil {
			c, err := FromWgQuick(decoded, name)
			if err == nil {
				return c, nil
			}
		}
	}
	return nil, firstErr
}
