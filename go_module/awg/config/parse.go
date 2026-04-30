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
	"i1":    struct{}{},
	"i2":    struct{}{},
	"i3":    struct{}{},
	"i4":    struct{}{},
	"i5":    struct{}{},
	"j1":    struct{}{},
	"j2":    struct{}{},
	"j3":    struct{}{},
	"itime": struct{}{},
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
		cidr, err := strconv.ParseUint(cidrStr, 0, 8)
		if err != nil {
			return nil, err
		} else if cidr > 128 {
			return nil, fmt.Errorf("cidr > 128")
		} else if cidr > 32 && maybeV4 != nil {
			return nil, fmt.Errorf("invalid network prefix length")
		}
		return &IPCidr{addr, uint8(cidr)}, nil
	} else {
		if maybeV4 != nil {
			return &IPCidr{addr, 32}, nil
		}
		return &IPCidr{addr, 128}, nil
	}
}

func parseEndpoint(s string) (*Endpoint, error) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return nil, fmt.Errorf("missing port from endpoint")
	}
	hostStr, portStr := s[:i], s[i+1:]
	if hostStr == "" {
		return nil, fmt.Errorf("invalid endpoint host")
	}
	port, err := parsePort(portStr)
	if err != nil {
		return nil, err
	}
	host, err := parseHost(hostStr)
	if err != nil {
		return nil, err
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

func parseHost(host string) (string, error) {
	hostColon := strings.IndexByte(host, ':')
	if host[0] != '[' && host[len(host)-1] != ']' && hostColon <= 0 {
		return host, nil
	} else {
		if len(host) > 3 && host[0] == '[' && host[len(host)-1] == ']' && hostColon > 0 {
			end := len(host) - 1
			if i := strings.LastIndexByte(host, '%'); i > 1 {
				end = i
			}
			maybeV6 := net.ParseIP(host[1:end])
			if maybeV6 == nil || len(maybeV6) != net.IPv6len {
				return "", fmt.Errorf("brackets must contain an IPv6 address")
			} else {
				host = host[1 : len(host)-1]
				return host, nil
			}
		} else {
			return "", fmt.Errorf("brackets must contain an IPv6 address")
		}
	}
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

func parseHStringSingle(name string, valueStr string) (HString, error) {
	m, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		return HString{}, err
	}
	if m < 0 || m > math.MaxUint32 {
		return HString{}, fmt.Errorf("invalid %s", name)
	}
	return Either[uint32, Pair[uint32, uint32]]{
		Left:   uint32(m),
		IsLeft: true,
	}, nil
}

func parseHStringRange(name, minRangeStr, maxRangeStr string) (HString, error) {
	minRange, err := strconv.ParseInt(minRangeStr, 10, 64)
	if err != nil {
		return HString{}, err
	}
	maxRange, err := strconv.ParseInt(maxRangeStr, 10, 64)
	if err != nil {
		return HString{}, err
	}
	if minRange < 0 || maxRange > math.MaxUint32 || maxRange <= minRange {
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

func parseHString(value, name string) (HString, error) {
	splitResult := strings.Split(value, "-")

	if len(splitResult) == 1 {
		return parseHStringSingle(name, splitResult[0])
	} else if len(splitResult) == 2 {
		return parseHStringRange(name, splitResult[0], splitResult[1])
	} else {
		return HString{}, fmt.Errorf("invalid %s", name)
	}
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

type ParserState int

const (
	inInterfaceSection ParserState = iota
	inPeerSection
	notInASection
)

type WgParserContext struct {
	conf          Config
	peer          *Peer
	parserState   ParserState
	sawPrivateKey bool
}

func (ctx *WgParserContext) applyInterfaceLine(key, val, line string) error {
	switch key {
	case "privatekey":
		k, err := parseKeyBase64(val)
		if err != nil {
			return err
		}
		ctx.conf.Interface.PrivateKey = *k
		ctx.sawPrivateKey = true
	case "jc":
		junkPacketCount, err := parseUint16(val, "junkPacketCount")
		if err != nil {
			return err
		}
		ctx.conf.Interface.JunkPacketCount = junkPacketCount
	case "jmin":
		junkPacketMinSize, err := parseUint16(val, "junkPacketMinSize")
		if err != nil {
			return err
		}
		ctx.conf.Interface.JunkPacketMinSize = junkPacketMinSize
	case "jmax":
		junkPacketMaxSize, err := parseUint16(val, "junkPacketMaxSize")
		if err != nil {
			return err
		}
		ctx.conf.Interface.JunkPacketMaxSize = junkPacketMaxSize
	case "s1":
		initPacketJunkSize, err := parseUint16(
			val,
			"initPacketJunkSize",
		)
		if err != nil {
			return err
		}
		ctx.conf.Interface.InitPacketJunkSize = initPacketJunkSize
	case "s2":
		responsePacketJunkSize, err := parseUint16(
			val,
			"responsePacketJunkSize",
		)
		if err != nil {
			return err
		}
		ctx.conf.Interface.ResponsePacketJunkSize = responsePacketJunkSize
	case "s3":
		cookieReplyJunkSize, err := parseUint16(
			val,
			"cookieReplyPacketJunkSize",
		)
		if err != nil {
			return err
		}
		ctx.conf.Interface.CookieReplyPacketJunkSize = cookieReplyJunkSize
	case "s4":
		transportJunkSize, err := parseUint16(
			val,
			"transportPacketJunkSize",
		)
		if err != nil {
			return err
		}
		ctx.conf.Interface.TransportPacketJunkSize = transportJunkSize
	case "h1":
		initPacketMagicHeader, err := parseHString(val, "initPacketMagicHeader")
		if err != nil {
			return err
		}
		ctx.conf.Interface.InitPacketMagicHeader = initPacketMagicHeader
	case "h2":
		responsePacketMagicHeader, err := parseHString(val, "responsePacketMagicHeader")
		if err != nil {
			return err
		}
		ctx.conf.Interface.ResponsePacketMagicHeader = responsePacketMagicHeader
	case "h3":
		underloadPacketMagicHeader, err := parseHString(val, "underloadPacketMagicHeader")
		if err != nil {
			return err
		}
		ctx.conf.Interface.UnderloadPacketMagicHeader = underloadPacketMagicHeader
	case "h4":
		transportPacketMagicHeader, err := parseHString(val, "transportPacketMagicHeader")
		if err != nil {
			return err
		}
		ctx.conf.Interface.TransportPacketMagicHeader = transportPacketMagicHeader
	case "i1", "i2", "i3", "i4", "i5":
		if val == "" {
			return nil
		}
		if ctx.conf.Interface.IPackets == nil {
			ctx.conf.Interface.IPackets = make(map[string]string)
		}
		ctx.conf.Interface.IPackets[key] = val
	case "mtu":
		m, err := parseMTU(val)
		if err != nil {
			return err
		}
		ctx.conf.Interface.MTU = m
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
			ctx.conf.Interface.Addresses = append(ctx.conf.Interface.Addresses, *a)
		}
	case "dns":
		addresses, err := splitList(val)
		if err != nil {
			return err
		}
		for _, address := range addresses {
			a := net.ParseIP(address)
			if a == nil {
				ctx.conf.Interface.DNSSearch = append(ctx.conf.Interface.DNSSearch, address)
			} else {
				ctx.conf.Interface.DNS = append(ctx.conf.Interface.DNS, a)
			}
		}
	default:
		return fmt.Errorf("invalid key for [Interface] section")
	}
	return nil
}

func (ctx *WgParserContext) applyPeerLine(key, val, line string) error {
	switch key {
	case "publickey":
		k, err := parseKeyBase64(val)
		if err != nil {
			return err
		}
		ctx.peer.PublicKey = *k
	case "presharedkey":
		k, err := parseKeyBase64(val)
		if err != nil {
			return err
		}
		ctx.peer.PresharedKey = *k
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
			ctx.peer.AllowedIPs = append(ctx.peer.AllowedIPs, *a)
		}
	case "persistentkeepalive":
		p, err := parsePersistentKeepalive(val)
		if err != nil {
			return err
		}
		ctx.peer.PersistentKeepalive = p
	case "endpoint":
		e, err := parseEndpoint(val)
		if err != nil {
			return err
		}
		ctx.peer.Endpoint = *e
	default:
		return fmt.Errorf("invalid key for [Peer] section")
	}
	return nil
}

func FromWgQuick(s string, name string) (*Config, error) {
	if !TunnelNameIsValid(name) {
		return nil, fmt.Errorf("tunnel name is not valid")
	}
	ctx := WgParserContext{
		parserState:   notInASection,
		conf:          Config{Name: name},
		sawPrivateKey: false,
	}
	lines := strings.Split(s, "\n")
	ctx.conf.Interface.MTU = 1420
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
			ctx.conf.MaybeAddPeer(ctx.peer)
			ctx.parserState = inInterfaceSection
			continue
		}
		if lineLower == "[peer]" {
			ctx.conf.MaybeAddPeer(ctx.peer)
			ctx.peer = &Peer{}
			ctx.parserState = inPeerSection
			continue
		}
		if ctx.parserState == notInASection {
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
		if ctx.parserState == inInterfaceSection {
			err := ctx.applyInterfaceLine(key, val, line)
			if err != nil {
				return nil, err
			} else {
				continue
			}
		} else if ctx.parserState == inPeerSection {
			err := ctx.applyPeerLine(key, val, line)
			if err != nil {
				return nil, err
			} else {
				continue
			}
		}
	}
	ctx.conf.MaybeAddPeer(ctx.peer)

	if !ctx.sawPrivateKey {
		return nil, fmt.Errorf("an interface must have a private key [none specified]")
	}
	for _, p := range ctx.conf.Peers {
		if p.PublicKey.IsZero() {
			return nil, fmt.Errorf("all peers must have public keys [none specified]")
		}
	}

	return &ctx.conf, nil
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
