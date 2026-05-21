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

const (
	interfacePrivateKey = "privatekey"
	interfaceJMin       = "jmin"
	interfaceJMax       = "jmax"
)

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
		host, err = parseBracketedIPv6Host(host, hostColon)
		if err != nil {
			return nil, err
		}
	}
	return &Endpoint{host, port}, nil
}

func parseBracketedIPv6Host(host string, hostColon int) (string, error) {
	err := fmt.Errorf("brackets must contain an IPv6 address")
	if len(host) <= 3 || host[0] != '[' || host[len(host)-1] != ']' || hostColon <= 0 {
		return "", err
	}
	end := len(host) - 1
	if i := strings.LastIndexByte(host, '%'); i > 1 {
		end = i
	}
	maybeV6 := net.ParseIP(host[1:end])
	if maybeV6 == nil || len(maybeV6) != net.IPv6len {
		return "", err
	}
	return host[1 : len(host)-1], nil
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
	case interfacePrivateKey, "mtu", "address", "dns", "i1", "i2", "i3", "i4", "i5":
		return parseInterfaceCoreField(conf, key, val)
	case "jc", interfaceJMin, interfaceJMax, "s1", "s2", "s3", "s4":
		return parseInterfaceJunkField(conf, key, val)
	case "h1", "h2", "h3", "h4":
		return parseInterfaceHeaderField(conf, key, val)
	default:
		return fmt.Errorf("invalid key for [Interface] section")
	}
}

func parseInterfaceCoreField(conf *Config, key, val string) error {
	switch key {
	case interfacePrivateKey:
		k, err := parseKeyBase64(val)
		if err != nil {
			return err
		}
		conf.Interface.PrivateKey = *k
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

func parseInterfaceJunkField(conf *Config, key, val string) error {
	parsed, err := parseUint16(val, interfaceJunkFieldName(key))
	if err != nil {
		return err
	}
	switch key {
	case "jc":
		conf.Interface.JunkPacketCount = parsed
	case interfaceJMin:
		conf.Interface.JunkPacketMinSize = parsed
	case interfaceJMax:
		conf.Interface.JunkPacketMaxSize = parsed
	case "s1":
		conf.Interface.InitPacketJunkSize = parsed
	case "s2":
		conf.Interface.ResponsePacketJunkSize = parsed
	case "s3":
		conf.Interface.CookieReplyPacketJunkSize = parsed
	case "s4":
		conf.Interface.TransportPacketJunkSize = parsed
	default:
		return fmt.Errorf("invalid key for [Interface] section")
	}
	return nil
}

func interfaceJunkFieldName(key string) string {
	switch key {
	case "jc":
		return "junkPacketCount"
	case interfaceJMin:
		return "junkPacketMinSize"
	case interfaceJMax:
		return "junkPacketMaxSize"
	case "s1":
		return "initPacketJunkSize"
	case "s2":
		return "responsePacketJunkSize"
	case "s3":
		return "cookieReplyPacketJunkSize"
	case "s4":
		return "transportPacketJunkSize"
	default:
		return key
	}
}

func parseInterfaceHeaderField(conf *Config, key, val string) error {
	parsed, err := parseHString(val, interfaceHeaderFieldName(key))
	if err != nil {
		return err
	}
	switch key {
	case "h1":
		conf.Interface.InitPacketMagicHeader = parsed
	case "h2":
		conf.Interface.ResponsePacketMagicHeader = parsed
	case "h3":
		conf.Interface.UnderloadPacketMagicHeader = parsed
	case "h4":
		conf.Interface.TransportPacketMagicHeader = parsed
	default:
		return fmt.Errorf("invalid key for [Interface] section")
	}
	return nil
}

func interfaceHeaderFieldName(key string) string {
	switch key {
	case "h1":
		return "initPacketMagicHeader"
	case "h2":
		return "responsePacketMagicHeader"
	case "h3":
		return "underloadPacketMagicHeader"
	case "h4":
		return "transportPacketMagicHeader"
	default:
		return key
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

func FromWgQuick(s, name string) (*Config, error) {
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
		switch parserState {
		case inInterfaceSection:
			if err := parseInterfaceField(&conf, key, val); err != nil {
				return nil, err
			}
			if key == interfacePrivateKey {
				sawPrivateKey = true
			}
		case inPeerSection:
			if err := parsePeerField(peer, key, val); err != nil {
				return nil, err
			}
		case notInASection:
			return nil, fmt.Errorf("line must occur in a section")
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
