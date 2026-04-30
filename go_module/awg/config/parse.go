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
	switch cidrStr {
	case "":
		switch maybeV4 {
		case nil:
			return &IPCidr{addr, 128}, nil
		default:
			return &IPCidr{addr, 32}, nil
		}
	default:
		cidr, err := strconv.ParseUint(cidrStr, 0, 8)
		if err != nil {
			return nil, err
		}

		switch {
		case cidr > 128:
			return nil, fmt.Errorf("cidr > 128")
		case cidr > 32 && maybeV4 != nil:
			return nil, fmt.Errorf("invalid network prefix length")
		default:
			return &IPCidr{addr, uint8(cidr)}, nil
		}
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

func parseIpv6Host(host string) (string, error) {
	hostColon := strings.IndexByte(host, ':')
	if len(host) > 3 && host[0] == '[' && host[len(host)-1] == ']' && hostColon > 0 {
		end := len(host) - 1
		if i := strings.LastIndexByte(host, '%'); i > 1 {
			end = i
		}
		maybeV6 := net.ParseIP(host[1:end])
		if maybeV6 == nil || len(maybeV6) != net.IPv6len {
			return "", fmt.Errorf("brackets must contain an IPv6 address")
		}
		host = host[1 : len(host)-1]
		return host, nil
	}
	return "", fmt.Errorf("brackets must contain an IPv6 address")
}

func parseHost(host string) (string, error) {
	hostColon := strings.IndexByte(host, ':')
	if host[0] != '[' && host[len(host)-1] != ']' && hostColon <= 0 {
		return host, nil
	}
	return parseIpv6Host(host)
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

func parseHStringSingle(name, valueStr string) (HString, error) {
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
			First:  uint32(minRange), // #nosec G115
			Second: uint32(maxRange), // #nosec G115
		},
		IsLeft: false,
	}, nil
}

func parseHString(value, name string) (HString, error) {
	splitResult := strings.Split(value, "-")

	switch len(splitResult) {
	case 1:
		return parseHStringSingle(name, splitResult[0])
	case 2:
		return parseHStringRange(name, splitResult[0], splitResult[1])
	default:
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

func (ctx *WgParserContext) applyPrivateKey(val string) error {
	k, err := parseKeyBase64(val)
	if err != nil {
		return err
	}
	ctx.conf.Interface.PrivateKey = *k
	ctx.sawPrivateKey = true
	return nil
}

func (ctx *WgParserContext) applyJc(val string) error {
	junkPacketCount, err := parseUint16(val, "junkPacketCount")
	if err != nil {
		return err
	}
	ctx.conf.Interface.JunkPacketCount = junkPacketCount
	return nil
}

func (ctx *WgParserContext) applyJmin(val string) error {
	junkPacketMinSize, err := parseUint16(val, "junkPacketMinSize")
	if err != nil {
		return err
	}
	ctx.conf.Interface.JunkPacketMinSize = junkPacketMinSize
	return nil
}

func (ctx *WgParserContext) applyJmax(val string) error {

	junkPacketMaxSize, err := parseUint16(val, "junkPacketMaxSize")
	if err != nil {
		return err
	}
	ctx.conf.Interface.JunkPacketMaxSize = junkPacketMaxSize
	return nil
}

func (ctx *WgParserContext) applyS1(val string) error {
	initPacketJunkSize, err := parseUint16(val, "initPacketJunkSize")
	if err != nil {
		return err
	}
	ctx.conf.Interface.InitPacketJunkSize = initPacketJunkSize
	return nil
}

func (ctx *WgParserContext) applyS2(val string) error {

	responsePacketJunkSize, err := parseUint16(val, "responsePacketJunkSize")
	if err != nil {
		return err
	}
	ctx.conf.Interface.ResponsePacketJunkSize = responsePacketJunkSize
	return nil
}

func (ctx *WgParserContext) applyS3(val string) error {
	cookieReplyJunkSize, err := parseUint16(val, "cookieReplyPacketJunkSize")
	if err != nil {
		return err
	}
	ctx.conf.Interface.CookieReplyPacketJunkSize = cookieReplyJunkSize
	return nil
}

func (ctx *WgParserContext) applyS4(val string) error {
	transportJunkSize, err := parseUint16(val, "transportPacketJunkSize")
	if err != nil {
		return err
	}
	ctx.conf.Interface.TransportPacketJunkSize = transportJunkSize
	return nil
}

func (ctx *WgParserContext) applyH1(val string) error {
	initPacketMagicHeader, err := parseHString(val, "initPacketMagicHeader")
	if err != nil {
		return err
	}
	ctx.conf.Interface.InitPacketMagicHeader = initPacketMagicHeader
	return nil
}

func (ctx *WgParserContext) applyH2(val string) error {
	responsePacketMagicHeader, err := parseHString(val, "responsePacketMagicHeader")
	if err != nil {
		return err
	}
	ctx.conf.Interface.ResponsePacketMagicHeader = responsePacketMagicHeader
	return nil
}

func (ctx *WgParserContext) applyH3(val string) error {
	underloadPacketMagicHeader, err := parseHString(val, "underloadPacketMagicHeader")
	if err != nil {
		return err
	}
	ctx.conf.Interface.UnderloadPacketMagicHeader = underloadPacketMagicHeader
	return nil
}

func (ctx *WgParserContext) applyH4(val string) error {
	transportPacketMagicHeader, err := parseHString(val, "transportPacketMagicHeader")
	if err != nil {
		return err
	}
	ctx.conf.Interface.TransportPacketMagicHeader = transportPacketMagicHeader
	return nil
}

func (ctx *WgParserContext) applyI(key, val string) error {
	if val == "" {
		return nil
	}
	if ctx.conf.Interface.IPackets == nil {
		ctx.conf.Interface.IPackets = make(map[string]string)
	}
	ctx.conf.Interface.IPackets[key] = val
	return nil
}

func (ctx *WgParserContext) applyMTU(val string) error {
	m, err := parseMTU(val)
	if err != nil {
		return err
	}
	ctx.conf.Interface.MTU = m
	return nil
}

func (ctx *WgParserContext) applyDNS(val string) error {
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
	return nil
}

func (ctx *WgParserContext) applyAddress(val string) error {
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
	return nil
}

func (ctx *WgParserContext) applyInterfaceLine(key, val string) error {
	switch key {
	case "privatekey":
		return ctx.applyPrivateKey(val)
	case "jc":
		return ctx.applyJc(val)
	case "jmin":
		return ctx.applyJmin(val)
	case "jmax":
		return ctx.applyJmax(val)
	case "s1":
		return ctx.applyS1(val)
	case "s2":
		return ctx.applyS2(val)
	case "s3":
		return ctx.applyS3(val)
	case "s4":
		return ctx.applyS4(val)
	case "h1":
		return ctx.applyH1(val)
	case "h2":
		return ctx.applyH2(val)
	case "h3":
		return ctx.applyH3(val)
	case "h4":
		return ctx.applyH4(val)
	case "i1", "i2", "i3", "i4", "i5":
		return ctx.applyI(key, val)
	case "mtu":
		return ctx.applyMTU(val)
	case "address":
		return ctx.applyAddress(val)
	case "dns":
		return ctx.applyDNS(val)
	default:
		return fmt.Errorf("invalid key for [Interface] section")
	}
}

func (ctx *WgParserContext) applyPeerLine(key, val string) error {
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

func FromWgQuick(s, name string) (*Config, error) {
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
			err := ctx.applyInterfaceLine(key, val)
			if err != nil {
				return nil, err
			}
			continue
		} else if ctx.parserState == inPeerSection {
			err := ctx.applyPeerLine(key, val)
			if err != nil {
				return nil, err
			}
			continue
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
