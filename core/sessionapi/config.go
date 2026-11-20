package sessionapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"core/log"
	protocolconfig "core/protocol"
)

// Protocol payloads remain open and are validated by their existing
// protocol-specific normalizers and runtimes. TOML metadata preserves the
// order in which the root protocol array tables appeared.
type configRoot struct {
	LogsEndpoint string                   `toml:"logs_endpoint"`
	ExcludeIPs   excludeIPsConfig         `toml:"ExcludeIPs"`
	Outline      []map[string]interface{} `toml:"Outline"`
	Xray         []map[string]interface{} `toml:"Xray"`
	TrustTunnel  []map[string]interface{} `toml:"TrustTunnel"`
}

type excludeIPsConfig struct {
	IPs []string `toml:"IPs"`
}

type parsedConfig struct {
	digest       string
	profiles     []RuntimeProfile
	logsEndpoint string
}

const (
	configSectionOutline     = "Outline"
	configSectionXray        = "Xray"
	configSectionTrustTunnel = "TrustTunnel"
	maxConfigBytes           = 1 << 20
)

// ExcludeIPs sends the listed addresses outside the VPN. A per-country list
// is the usual content, so a single entry may be wide. An invalid address, a
// /0 entry, or entries of one family that together cover half of IPv4 or of
// global IPv6 (2000::/3) are a subscription mistake such as a typo or an
// inverted list. Such entries are dropped, so that traffic stays in the VPN,
// and the subscription is still accepted. Unique local and link-local IPv6
// entries never reach the internet and are never counted or dropped.
var (
	globalIPv6            = &net.IPNet{IP: net.ParseIP("2000::"), Mask: net.CIDRMask(3, 128)}
	localIPv6             = []*net.IPNet{{IP: net.ParseIP("fc00::"), Mask: net.CIDRMask(7, 128)}, {IP: net.ParseIP("fe80::"), Mask: net.CIDRMask(10, 128)}}
	maxExcludedIPv4       = new(big.Int).Lsh(big.NewInt(1), 31)  // half of IPv4
	maxExcludedGlobalIPv6 = new(big.Int).Lsh(big.NewInt(1), 124) // half of 2000::/3
)

func parseConfig(raw []byte) (parsedConfig, error) {
	root, protocols, err := decodeConfig(raw)
	if err != nil {
		return parsedConfig{}, err
	}
	var excludeWarnings []string
	root.ExcludeIPs.IPs, excludeWarnings = filterExcludeIPs(root.ExcludeIPs.IPs)
	profiles, warnings, err := parseProfiles(root, protocols)
	if err != nil {
		return parsedConfig{}, err
	}
	if validateErr := validateLogsEndpoint(root.LogsEndpoint); validateErr != nil {
		return parsedConfig{}, validateErr
	}
	// Warnings are logged only for a subscription that is accepted.
	for _, w := range warnings {
		w.ref.emit(w.items)
	}
	for _, message := range excludeWarnings {
		log.Warn(logCategoryConfig, message, nil)
	}
	digest := sha256.Sum256(raw)
	return parsedConfig{digest: hex.EncodeToString(digest[:]), profiles: profiles, logsEndpoint: root.LogsEndpoint}, nil
}

func validateLogsEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	u, parseErr := url.Parse(endpoint)
	if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || !strings.HasSuffix(u.EscapedPath(), "/") || u.RawQuery != "" || u.ForceQuery || strings.Contains(endpoint, "#") || u.Opaque != "" {
		return failure(FailureMalformedConfig, "logs_endpoint must be an HTTPS directory URL without query or fragment")
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return failure(FailureMalformedConfig, "logs_endpoint has an invalid port")
		}
	}
	if u.User != nil && strings.Contains(u.User.Username(), ":") {
		return failure(FailureMalformedConfig, "logs_endpoint Basic username cannot contain a colon")
	}
	return nil
}

// filterExcludeIPs drops the entries described above and returns the
// entries to use with warnings that carry counts, never addresses. Host
// names are kept; one that cannot be resolved is ignored at start.
func filterExcludeIPs(entries []string) ([]string, []string) {
	// family is 0 for host names and local IPv6, which are always kept.
	const ipv4, ipv6, invalid, defaultRoute = 4, 6, -1, -2
	family := make([]int, len(entries))
	var networks4, counted6 []*net.IPNet
	count := map[int]int{}
	for i, raw := range entries {
		entry := strings.TrimSpace(raw)
		_, network, err := net.ParseCIDR(entry)
		if ip := net.ParseIP(entry); err != nil && ip != nil {
			network = &net.IPNet{IP: ip, Mask: net.CIDRMask(8*len(ip), 8*len(ip))}
		}
		if network == nil {
			// Host names have no ":" or "/" and are not all digits and dots.
			if strings.ContainsAny(entry, ":/") || entry != "" && strings.Trim(entry, "0123456789.") == "" {
				family[i] = invalid
			}
			count[family[i]]++
			continue
		}
		ones, bits := network.Mask.Size()
		if ip := network.IP.To4(); ip != nil {
			// An IPv4-mapped prefix matches IPv4 destinations.
			ones -= bits - 32
			network = &net.IPNet{IP: ip, Mask: net.CIDRMask(ones, 32)}
			family[i] = ipv4
		} else if !insideAny(localIPv6, network) {
			family[i] = ipv6
		}
		switch {
		case ones == 0:
			family[i] = defaultRoute
		case family[i] == ipv4:
			networks4 = append(networks4, network)
		case family[i] == ipv6 && globalIPv6.Contains(network.IP):
			counted6 = append(counted6, network)
		case family[i] == ipv6 && network.Contains(globalIPv6.IP):
			counted6 = append(counted6, globalIPv6)
		}
		count[family[i]]++
	}
	drop := map[int]bool{invalid: true, defaultRoute: true}
	var warnings []string
	if count[invalid] > 0 {
		warnings = append(warnings, fmt.Sprintf("ExcludeIPs: dropped entries that are not valid addresses or networks (%d); this traffic stays in the VPN", count[invalid]))
	}
	if count[defaultRoute] > 0 {
		warnings = append(warnings, fmt.Sprintf("ExcludeIPs: dropped entries with prefix /0 (%d); this traffic stays in the VPN", count[defaultRoute]))
	}
	if covered := coveredAddresses(networks4); covered.Cmp(maxExcludedIPv4) >= 0 {
		drop[ipv4] = true
		warnings = append(warnings, fmt.Sprintf("ExcludeIPs: dropped all IPv4 entries (%d) because together they cover %s addresses, half of IPv4 or more; this traffic stays in the VPN", count[ipv4], covered))
	}
	if coveredAddresses(counted6).Cmp(maxExcludedGlobalIPv6) >= 0 {
		drop[ipv6] = true
		warnings = append(warnings, fmt.Sprintf("ExcludeIPs: dropped all global IPv6 entries (%d) because together they cover half of 2000::/3 or more; this traffic stays in the VPN", count[ipv6]))
	}
	kept := make([]string, 0, len(entries))
	for i, entry := range entries {
		if !drop[family[i]] {
			kept = append(kept, entry)
		}
	}
	return kept, warnings
}

func insideAny(parents []*net.IPNet, network *net.IPNet) bool {
	ones, _ := network.Mask.Size()
	for _, parent := range parents {
		if parentOnes, _ := parent.Mask.Size(); ones >= parentOnes && parent.Contains(network.IP) {
			return true
		}
	}
	return false
}

// coveredAddresses counts the addresses the networks cover, counting nested
// networks once. CIDR networks either nest or do not overlap.
func coveredAddresses(networks []*net.IPNet) *big.Int {
	sort.Slice(networks, func(i, j int) bool {
		if order := bytes.Compare(networks[i].IP, networks[j].IP); order != 0 {
			return order < 0
		}
		return bytes.Compare(networks[i].Mask, networks[j].Mask) < 0
	})
	total := new(big.Int)
	var last *net.IPNet
	for _, network := range networks {
		if last != nil && last.Contains(network.IP) {
			continue
		}
		ones, bits := network.Mask.Size()
		total.Add(total, new(big.Int).Lsh(big.NewInt(1), uint(bits-ones)))
		last = network
	}
	return total
}

func decodeConfig(raw []byte) (configRoot, []string, error) {
	if len(raw) > maxConfigBytes {
		return configRoot{}, nil, failure(FailureMalformedConfig, "configuration exceeds the 1 MiB size limit")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return configRoot{}, nil, failure(FailureMalformedConfig, "The subscription is empty. Expected TOML with at least one [[Outline]], [[Xray]] or [[TrustTunnel]] profile, see "+subscriptionFormatURL)
	}
	var root configRoot
	metadata, err := toml.Decode(string(raw), &root)
	if err != nil {
		return configRoot{}, nil, undecodableConfigError(raw, err)
	}
	if metadata.IsDefined("logs_endpoint") && root.LogsEndpoint == "" {
		return configRoot{}, nil, failure(FailureMalformedConfig, "logs_endpoint must not be empty")
	}
	if err := validateRootKeys(metadata.Keys()); err != nil {
		return configRoot{}, nil, err
	}

	var protocols []string
	for _, key := range metadata.Keys() {
		if len(key) != 1 || metadata.Type(key...) != "ArrayHash" {
			continue
		}
		switch key[0] {
		case configSectionOutline, configSectionXray, configSectionTrustTunnel:
			protocols = append(protocols, key[0])
		}
	}
	if len(protocols) == 0 {
		return configRoot{}, nil, failure(FailureMalformedConfig, "The subscription contains no profiles. Add at least one [[Outline]], [[Xray]] or [[TrustTunnel]] section, see "+subscriptionFormatURL)
	}

	counts := map[string]int{}
	for _, protocol := range protocols {
		counts[protocol]++
	}
	if counts[configSectionOutline] != len(root.Outline) || counts[configSectionXray] != len(root.Xray) || counts[configSectionTrustTunnel] != len(root.TrustTunnel) {
		return configRoot{}, nil, failure(FailureMalformedConfig, "protocol section count does not match TOML data")
	}
	return root, protocols, nil
}

type profileWarnings struct {
	ref   profileRef
	items []profileWarning
}

func parseProfiles(root configRoot, protocols []string) ([]RuntimeProfile, []profileWarnings, error) {
	next := map[string]int{}
	profiles := make([]RuntimeProfile, 0, len(protocols))
	var warnings []profileWarnings
	for index, protocolName := range protocols {
		profile, ref, items, err := parseProfile(root, next, protocolName, int32(index))
		if err != nil {
			return nil, nil, err
		}
		profiles = append(profiles, profile)
		if len(items) > 0 {
			warnings = append(warnings, profileWarnings{ref: ref, items: items})
		}
	}
	return profiles, warnings, nil
}

func parseProfile(root configRoot, next map[string]int, protocolName string, profileIndex int32) (RuntimeProfile, profileRef, []profileWarning, error) {
	block, protocol := nextProfile(root, next, protocolName)
	next[protocolName]++
	ref := profileRef{number: int(profileIndex) + 1, protocol: protocol}
	description := ""
	if value, present := block[fieldDescription]; present {
		var ok bool
		description, ok = value.(string)
		if !ok {
			return RuntimeProfile{}, ref, nil, ref.reject(invalidField(fieldDescription, reasonString))
		}
	}
	ref.description = description
	if cloakValue, present := block[fieldCloak]; present {
		cloak, ok := cloakValue.(bool)
		if !ok {
			return RuntimeProfile{}, ref, nil, ref.reject(invalidField(fieldCloak, reasonBoolean))
		}
		if cloak {
			return RuntimeProfile{}, ref, nil, ref.reject(&fieldError{code: FailureUnsupported, field: fieldCloak, reason: "removed Cloak profiles are not supported"})
		}
	}
	if protocol == ProtocolTrustTunnel {
		if err := validateTrustTunnelVerification(block); err != nil {
			return RuntimeProfile{}, ref, nil, ref.reject(err)
		}
	}
	normalized, err := normalizeProfile(protocol, block)
	if err != nil {
		return RuntimeProfile{}, ref, nil, ref.reject(err)
	}
	return RuntimeProfile{
		Summary:      ProfileSummary{Index: profileIndex, Protocol: protocol, Description: description},
		Config:       normalized,
		ExcludeCIDRs: append([]string(nil), root.ExcludeIPs.IPs...),
	}, ref, profileSettingWarnings(protocol, block), nil
}

func nextProfile(root configRoot, next map[string]int, name string) (map[string]interface{}, Protocol) {
	switch name {
	case configSectionOutline:
		return root.Outline[next[name]], ProtocolOutline
	case configSectionXray:
		return root.Xray[next[name]], ProtocolXray
	case configSectionTrustTunnel:
		return root.TrustTunnel[next[name]], ProtocolTrustTunnel
	default:
		return nil, ""
	}
}

func validateRootKeys(keys []toml.Key) error {
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		if key[0] == "logs_endpoint" && len(key) != 1 {
			return failure(FailureMalformedConfig, "logs_endpoint must be a URL string")
		}
		if key[0] == "ExcludeIPs" && len(key) > 1 && (key[1] != "IPs" || len(key) > 2) {
			return failure(FailureUnsupported, "configuration contains an unsupported ExcludeIPs setting")
		}
		switch key[0] {
		case configSectionOutline, configSectionXray, configSectionTrustTunnel, "ExcludeIPs", "logs_endpoint":
		default:
			return failure(FailureUnsupported, "configuration contains an unsupported section")
		}
	}
	return nil
}

func validateTrustTunnelVerification(block map[string]interface{}) error {
	if _, present := block[fieldSkipVerification]; present {
		return invalidField(fieldSkipVerification, "is only supported under endpoint")
	}
	endpointValue, hasEndpoint := block[fieldEndpoint]
	if !hasEndpoint {
		return nil
	}
	endpoint, ok := endpointValue.(map[string]interface{})
	if !ok {
		return invalidField(fieldEndpoint, "must be a table")
	}
	value, present := endpoint[fieldSkipVerification]
	if !present {
		return nil
	}
	skipVerification, ok := value.(bool)
	if !ok {
		return invalidField(fieldEndpointSkipVerification, reasonBoolean)
	}
	if skipVerification {
		return invalidField(fieldEndpointSkipVerification, "certificate verification must remain enabled")
	}
	return nil
}

func normalizeProfile(kind Protocol, block map[string]any) (protocolconfig.Config, error) {
	switch kind {
	case ProtocolXray:
		outbounds, ok := block[fieldOutbounds]
		if !ok {
			return protocolconfig.Config{}, invalidField(fieldOutbounds, reasonRequired)
		}
		switch outbounds.(type) {
		case []any, []map[string]any:
		default:
			return protocolconfig.Config{}, invalidField(fieldOutbounds, "must be an array")
		}
		if firstOutboundIsFreedom(outbounds) {
			return protocolconfig.Config{}, invalidField(fieldOutbounds, "the first outbound carries all traffic and must not use the freedom protocol, which sends it outside the VPN")
		}
		return protocolconfig.Config{Xray: xrayFields(block)}, nil
	case ProtocolOutline:
		normalized, err := normalizeOutlineURL(block)
		return protocolconfig.Config{OutlineURL: normalized}, err
	case ProtocolTrustTunnel:
		return protocolconfig.Config{TrustTunnel: protocolconfig.CloneFields(block)}, nil
	default:
		return protocolconfig.Config{}, failure(FailureUnsupported, "unsupported protocol")
	}
}

// xrayFields keeps only the Xray settings the client needs; the client adds
// its own inbound. Keys must match exactly: Xray decodes JSON keys
// case-insensitively, so another spelling would bypass the list.
func xrayFields(block map[string]any) map[string]any {
	cloned := protocolconfig.CloneFields(block)
	fields := map[string]any{}
	for _, key := range []string{"outbounds", "dns", "policy"} {
		if value, ok := cloned[key]; ok {
			fields[key] = value
		}
	}
	if logConfig, ok := cloned["log"].(map[string]any); ok {
		kept := map[string]any{}
		for _, key := range []string{"loglevel", "dnsLog", "maskAddress"} {
			if value, ok := logConfig[key]; ok {
				kept[key] = value
			}
		}
		fields["log"] = kept
	}
	return fields
}

// firstOutboundIsFreedom reports whether Xray's default outbound, the first
// one, sends traffic directly.
func firstOutboundIsFreedom(outbounds any) bool {
	var first any
	switch list := outbounds.(type) {
	case []any:
		if len(list) > 0 {
			first = list[0]
		}
	case []map[string]any:
		if len(list) > 0 {
			first = list[0]
		}
	}
	outbound, _ := first.(map[string]any)
	for key, value := range outbound {
		if protocol, _ := value.(string); strings.EqualFold(key, "protocol") && strings.EqualFold(protocol, "freedom") {
			return true
		}
	}
	return false
}

// normalizeOutlineURL is the Go equivalent of the legacy shell builder. It is
// intentionally kept here, before any platform binding sees configuration, so
// Android/iOS/desktop cannot diverge in method defaults, websocket paths, or
// transport URL escaping. Protocol validation remains the runtime's job.
func normalizeOutlineURL(block map[string]interface{}) (string, error) {
	method, err := outlineMethod(block)
	if err != nil {
		return "", err
	}
	password, err := outlinePassword(block)
	if err != nil {
		return "", err
	}
	server := stringValue(block, fieldServer)
	if server == "" {
		return "", invalidField(fieldServer, reasonRequired)
	}
	host, ok := outlineServerHost(server)
	if !ok {
		return "", invalidField(fieldServer, "must be a hostname or IP address")
	}
	websocket := false
	if value, present := block[fieldWebSocket]; present {
		websocket, ok = value.(bool)
		if !ok {
			return "", invalidField(fieldWebSocket, reasonBoolean)
		}
	}
	port, err := outlinePortField(block, websocket)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(method + ":" + password))
	ssURL := "ss://" + encoded + "@" + net.JoinHostPort(host, port)
	if prefix, _ := block[fieldDisguisePrefix].(string); prefix != "" {
		ssURL += "?prefix=" + url.QueryEscape(prefix)
	}
	if !websocket {
		return ssURL, nil
	}
	path, ok := block[fieldWebSocketPath].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", invalidField(fieldWebSocketPath, "must be a non-empty string when WebSocket = true")
	}
	base := strings.TrimRight(path, "/")
	if base != "" && !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	params := url.Values{"tcp_path": {base + "/tcp"}, "udp_path": {base + "/udp"}}
	return "tls:sni=" + host + "|ws:" + params.Encode() + "|" + ssURL, nil
}

// outlineMethod keeps the default cipher for an absent or blank Method; only a
// value that is not a string is rejected.
func outlineMethod(block map[string]interface{}) (string, error) {
	value, present := block[fieldMethod]
	text, ok := value.(string)
	if present && !ok {
		return "", invalidField(fieldMethod, reasonString)
	}
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		return trimmed, nil
	}
	return defaultOutlineMethod, nil
}

func outlinePassword(block map[string]interface{}) (string, error) {
	value, present := block[fieldPassword]
	if !present {
		return "", invalidField(fieldPassword, reasonRequired)
	}
	text, ok := value.(string)
	if !ok {
		return "", invalidField(fieldPassword, reasonString)
	}
	password := strings.TrimSpace(text)
	if password == "" {
		return "", invalidField(fieldPassword, reasonRequired)
	}
	return password, nil
}

func outlinePortField(block map[string]interface{}, websocket bool) (string, error) {
	value, present := block[fieldPort]
	if present {
		port, valid := outlinePort(value)
		if !valid {
			return "", invalidField(fieldPort, "must be an integer from 1 to 65535")
		}
		return port, nil
	}
	if websocket {
		return "443", nil
	}
	return "", invalidField(fieldPort, "is required unless WebSocket = true")
}

func outlinePort(value interface{}) (string, bool) {
	var number float64
	switch v := value.(type) {
	case int64:
		number = float64(v)
	case int:
		number = float64(v)
	case float64:
		number = v
	default:
		return "", false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 1 || number > 65535 || math.Trunc(number) != number {
		return "", false
	}
	return strconv.Itoa(int(number)), true
}

func outlineServerHost(server string) (string, bool) {
	if strings.HasPrefix(server, "[") && strings.HasSuffix(server, "]") {
		host := server[1 : len(server)-1]
		ip := net.ParseIP(host)
		return host, ip != nil && strings.Contains(host, ":")
	}
	if net.ParseIP(server) != nil {
		return server, true
	}
	if server == "" || len(server) > 253 {
		return "", false
	}
	for _, label := range strings.Split(strings.TrimSuffix(server, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", false
		}
		for _, r := range label {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
				return "", false
			}
		}
	}
	return server, true
}

func stringValue(block map[string]interface{}, key string) string {
	value, _ := block[key].(string)
	return strings.TrimSpace(value)
}
