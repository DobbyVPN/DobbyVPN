package georouting

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

type Outbound string

const (
	OutboundProxy  Outbound = "proxy"
	OutboundDirect Outbound = "direct"
	OutboundBlock  Outbound = "block"
)

type Network string

const (
	NetworkTCP  Network = "tcp"
	NetworkUDP  Network = "udp"
	NetworkICMP Network = "icmp"
	NetworkAny  Network = ""
)

type Metadata struct {
	SrcIP   netip.Addr
	DstIP   netip.Addr
	SrcPort uint16
	DstPort uint16
	Network Network
	Domain  string // опционально (SNI/Host), если где-то выше ты умеешь его “доснифать”
}

// Router — общий интерфейс, чтобы tunnel был независим от конкретной реализации.
type Router interface {
	Decide(meta Metadata) Outbound
}

type PortRange struct {
	From uint16 `json:"from"`
	To   uint16 `json:"to"`
}

// RuleConfig — конфиг правила (можно грузить из JSON).
// Это сознательно «подмножество» идей sing-box: domain/domain_suffix/ip_cidr/port/port_range/network/ip_version.
type RuleConfig struct {
	Outbound Outbound `json:"outbound"`

	Network   []Network `json:"network,omitempty"`    // ["tcp","udp"]
	IPVersion int       `json:"ip_version,omitempty"` // 4 или 6

	Domain       []string `json:"domain,omitempty"`        // точные домены
	DomainSuffix []string `json:"domain_suffix,omitempty"` // суффиксы (".example.com" или "example.com" = будет нормализовано)

	DomainFile []string `json:"domain_file,omitempty"` // путь(и) к файлам со списками доменов

	IPCIDR     []string `json:"ip_cidr,omitempty"`      // ["1.2.3.0/24"]
	IPCIDRFile []string `json:"ip_cidr_file,omitempty"` // путь(и) к файлам со списками CIDR

	Port      []uint16    `json:"port,omitempty"`
	PortRange []PortRange `json:"port_range,omitempty"`

	// Если задано, то правило может матчить private/non-private IP как часть доменно-IP группы.
	IPIsPrivate *bool `json:"ip_is_private,omitempty"`
}

type Config struct {
	Rules []RuleConfig `json:"rules"`
	Final Outbound     `json:"final"` // fallback, если ничего не совпало (по умолчанию proxy)
}

type Engine struct {
	rules []compiledRule
	final Outbound
}

type netMask uint8

const (
	maskTCP netMask = 1 << iota
	maskUDP
	maskICMP
)

type compiledRule struct {
	outbound Outbound
	netMask  netMask
	ipVer    int // 0/4/6

	domainExact  map[string]struct{}
	domainSuffix []string

	ipTrie      *IPTrie
	ipIsPrivate *bool

	portSet   map[uint16]struct{}
	portRange []PortRange
}

func NewEngine(cfg Config) (*Engine, error) {
	final := cfg.Final
	if final == "" {
		final = OutboundProxy
	}

	var rules []compiledRule
	for i, rc := range cfg.Rules {
		if rc.Outbound == "" {
			return nil, fmt.Errorf("rule[%d]: outbound is required", i)
		}
		cr := compiledRule{
			outbound: rc.Outbound,
			ipVer:    rc.IPVersion,
		}
		cr.netMask = compileNetMask(rc.Network)

		if cr.ipVer != 0 && cr.ipVer != 4 && cr.ipVer != 6 {
			return nil, fmt.Errorf("rule[%d]: ip_version must be 0/4/6", i)
		}

		// Домены
		if len(rc.Domain) > 0 {
			cr.domainExact = make(map[string]struct{}, len(rc.Domain))
			for _, d := range rc.Domain {
				d = normalizeDomain(d)
				if d == "" {
					continue
				}
				cr.domainExact[d] = struct{}{}
			}
		}
		for _, suf := range rc.DomainSuffix {
			suf = normalizeDomain(suf)
			if suf == "" {
				continue
			}
			// доменный суффикс храним как ".example.com"
			if !strings.HasPrefix(suf, ".") {
				suf = "." + suf
			}
			cr.domainSuffix = append(cr.domainSuffix, suf)
		}
		for _, path := range rc.DomainFile {
			lines, err := readListFile(path)
			if err != nil {
				return nil, fmt.Errorf("rule[%d]: read domain_file %q: %w", i, path, err)
			}
			if cr.domainExact == nil && len(lines) > 0 {
				cr.domainExact = make(map[string]struct{}, len(lines))
			}
			for _, line := range lines {
				d := normalizeDomain(line)
				if d == "" {
					continue
				}
				// Поддержка "*.example.com" и ".example.com"
				if strings.HasPrefix(d, "*.") {
					d = d[1:] // "*.x" -> ".x"
				}
				if strings.HasPrefix(d, ".") {
					cr.domainSuffix = append(cr.domainSuffix, d)
					continue
				}
				cr.domainExact[d] = struct{}{}
			}
		}

		// IP CIDR
		var cidrs []string
		cidrs = append(cidrs, rc.IPCIDR...)
		for _, path := range rc.IPCIDRFile {
			lines, err := readListFile(path)
			if err != nil {
				return nil, fmt.Errorf("rule[%d]: read ip_cidr_file %q: %w", i, path, err)
			}
			cidrs = append(cidrs, lines...)
		}
		if len(cidrs) > 0 {
			cr.ipTrie = NewIPTrie()
			for _, s := range cidrs {
				s = strings.TrimSpace(s)
				if s == "" {
					continue
				}
				pfx, err := netip.ParsePrefix(s)
				if err != nil {
					return nil, fmt.Errorf("rule[%d]: bad cidr %q: %w", i, s, err)
				}
				cr.ipTrie.Add(pfx)
			}
		}

		// Private IP match
		cr.ipIsPrivate = rc.IPIsPrivate

		// Порты
		if len(rc.Port) > 0 {
			cr.portSet = make(map[uint16]struct{}, len(rc.Port))
			for _, p := range rc.Port {
				cr.portSet[p] = struct{}{}
			}
		}
		if len(rc.PortRange) > 0 {
			for _, pr := range rc.PortRange {
				if pr.From == 0 || pr.To == 0 || pr.From > pr.To {
					return nil, fmt.Errorf("rule[%d]: bad port_range %+v", i, pr)
				}
				cr.portRange = append(cr.portRange, pr)
			}
		}

		rules = append(rules, cr)
	}

	return &Engine{rules: rules, final: final}, nil
}

func (e *Engine) Decide(meta Metadata) Outbound {
	for _, r := range e.rules {
		if r.match(meta) {
			return r.outbound
		}
	}
	return e.final
}

func (r *compiledRule) match(meta Metadata) bool {
	// network
	if r.netMask != 0 {
		if !r.matchNetwork(meta.Network) {
			return false
		}
	}

	// ip version
	if r.ipVer == 4 && !meta.DstIP.Unmap().Is4() {
		return false
	}
	if r.ipVer == 6 && !meta.DstIP.Unmap().Is6() {
		return false
	}

	// domain/ip group — как “domain||...||ip_cidr||ip_is_private” у sing-box: если хоть один критерий в группе задан, то должен совпасть хотя бы один.
	if r.hasDomainIPGroup() {
		ok := false

		d := normalizeDomain(meta.Domain)
		if d != "" {
			if r.domainExact != nil {
				if _, hit := r.domainExact[d]; hit {
					ok = true
				}
			}
			if !ok && len(r.domainSuffix) > 0 {
				for _, suf := range r.domainSuffix {
					if strings.HasSuffix(d, suf) {
						ok = true
						break
					}
				}
			}
		}

		if !ok && r.ipTrie != nil {
			if meta.DstIP.IsValid() && r.ipTrie.Contains(meta.DstIP) {
				ok = true
			}
		}

		if !ok && r.ipIsPrivate != nil {
			if meta.DstIP.IsValid() && (meta.DstIP.IsPrivate() == *r.ipIsPrivate) {
				ok = true
			}
		}

		if !ok {
			return false
		}
	}

	// port group — (port || port_range)
	if r.hasPortGroup() {
		if meta.DstPort == 0 {
			return false
		}
		ok := false
		if r.portSet != nil {
			if _, hit := r.portSet[meta.DstPort]; hit {
				ok = true
			}
		}
		if !ok && len(r.portRange) > 0 {
			for _, pr := range r.portRange {
				if meta.DstPort >= pr.From && meta.DstPort <= pr.To {
					ok = true
					break
				}
			}
		}
		if !ok {
			return false
		}
	}

	return true
}

func (r *compiledRule) hasDomainIPGroup() bool {
	return r.domainExact != nil || len(r.domainSuffix) > 0 || r.ipTrie != nil || r.ipIsPrivate != nil
}

func (r *compiledRule) hasPortGroup() bool {
	return r.portSet != nil || len(r.portRange) > 0
}

func (r *compiledRule) matchNetwork(n Network) bool {
	switch n {
	case NetworkTCP:
		return r.netMask&maskTCP != 0
	case NetworkUDP:
		return r.netMask&maskUDP != 0
	case NetworkICMP:
		return r.netMask&maskICMP != 0
	default:
		return false
	}
}

func compileNetMask(ns []Network) netMask {
	var m netMask
	for _, n := range ns {
		switch n {
		case NetworkTCP:
			m |= maskTCP
		case NetworkUDP:
			m |= maskUDP
		case NetworkICMP:
			m |= maskICMP
		}
	}
	return m
}

func normalizeDomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, ".")
	return s
}

func readListFile(path string) ([]string, error) {
	if path == "" {
		return nil, errors.New("empty path")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

func LoadConfigJSONFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
