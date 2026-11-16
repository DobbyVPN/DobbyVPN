package sessionapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

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

func parseConfig(raw []byte) (parsedConfig, error) {
	root, protocols, err := decodeConfig(raw)
	if err != nil {
		return parsedConfig{}, err
	}
	profiles, err := parseProfiles(root, protocols)
	if err != nil {
		return parsedConfig{}, err
	}
	if validateErr := validateLogsEndpoint(root.LogsEndpoint); validateErr != nil {
		return parsedConfig{}, validateErr
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

func decodeConfig(raw []byte) (configRoot, []string, error) {
	if len(raw) > maxConfigBytes {
		return configRoot{}, nil, failure(FailureMalformedConfig, "configuration exceeds the 1 MiB size limit")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return configRoot{}, nil, failure(FailureMalformedConfig, "configuration is blank")
	}
	var root configRoot
	metadata, err := toml.Decode(string(raw), &root)
	if err != nil {
		return configRoot{}, nil, failureWithCause(FailureMalformedConfig, "TOML could not be parsed", err)
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
		return configRoot{}, nil, failure(FailureMalformedConfig, "expected one or more [[Outline]], [[Xray]], or [[TrustTunnel]] sections")
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

func parseProfiles(root configRoot, protocols []string) ([]RuntimeProfile, error) {
	next := map[string]int{}
	profiles := make([]RuntimeProfile, 0, len(protocols))
	for index, protocolName := range protocols {
		profile, err := parseProfile(root, next, protocolName, int32(index))
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func parseProfile(root configRoot, next map[string]int, protocolName string, profileIndex int32) (RuntimeProfile, error) {
	block, protocol := nextProfile(root, next, protocolName)
	next[protocolName]++
	if cloakValue, present := block["Cloak"]; present {
		cloak, ok := cloakValue.(bool)
		if !ok {
			return RuntimeProfile{}, failure(FailureMalformedConfig, "Cloak must be a boolean")
		}
		if cloak {
			return RuntimeProfile{}, failure(FailureUnsupported, "configuration contains a removed Cloak profile")
		}
	}
	if protocol == ProtocolTrustTunnel {
		if err := validateTrustTunnelVerification(block); err != nil {
			return RuntimeProfile{}, err
		}
	}
	description := ""
	if value, present := block["Description"]; present {
		var ok bool
		description, ok = value.(string)
		if !ok {
			return RuntimeProfile{}, failure(FailureMalformedConfig, "Description must be a string")
		}
	}
	normalized, err := normalizeProfile(protocol, block)
	if err != nil {
		return RuntimeProfile{}, err
	}
	return RuntimeProfile{
		Summary:      ProfileSummary{Index: profileIndex, Protocol: protocol, Description: description},
		Config:       normalized,
		ExcludeCIDRs: append([]string(nil), root.ExcludeIPs.IPs...),
	}, nil
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
	if _, present := block["skip_verification"]; present {
		return failure(FailureMalformedConfig, "TrustTunnel skip_verification is only supported under endpoint")
	}
	endpoint, ok := block["endpoint"].(map[string]interface{})
	if !ok {
		return nil
	}
	value, present := endpoint["skip_verification"]
	if !present {
		return nil
	}
	skipVerification, ok := value.(bool)
	if !ok {
		return failure(FailureMalformedConfig, "TrustTunnel endpoint.skip_verification must be a boolean")
	}
	if skipVerification {
		return failure(FailureMalformedConfig, "TrustTunnel certificate verification must remain enabled")
	}
	return nil
}

func normalizeProfile(kind Protocol, block map[string]any) (protocolconfig.Config, error) {
	switch kind {
	case ProtocolXray:
		if _, ok := block["outbounds"]; !ok {
			return protocolconfig.Config{}, failure(FailureMalformedConfig, "Xray profile requires outbounds")
		}
		return protocolconfig.Config{Xray: protocolconfig.CloneFields(block)}, nil
	case ProtocolOutline:
		normalized, err := normalizeOutlineURL(block)
		return protocolconfig.Config{OutlineURL: normalized}, err
	case ProtocolTrustTunnel:
		return protocolconfig.Config{TrustTunnel: protocolconfig.CloneFields(block)}, nil
	default:
		return protocolconfig.Config{}, failure(FailureUnsupported, "unsupported protocol")
	}
}

// normalizeOutlineURL is the Go equivalent of the legacy shell builder. It is
// intentionally kept here, before any platform binding sees configuration, so
// Android/iOS/desktop cannot diverge in method defaults, websocket paths, or
// transport URL escaping. Protocol validation remains the runtime's job.
func normalizeOutlineURL(block map[string]interface{}) (string, error) {
	method := stringValue(block, "Method")
	if method == "" {
		method = "chacha20-ietf-poly1305"
	}
	password := stringValue(block, "Password")
	if password == "" {
		return "", failure(FailureMalformedConfig, "Outline profile requires a password")
	}
	server := stringValue(block, "Server")
	host, ok := outlineServerHost(server)
	if !ok {
		return "", failure(FailureMalformedConfig, "Outline server must be a hostname or IP address")
	}
	websocket := false
	if value, present := block["WebSocket"]; present {
		var valid bool
		websocket, valid = value.(bool)
		if !valid {
			return "", failure(FailureMalformedConfig, "Outline WebSocket must be a boolean")
		}
	}
	port := ""
	if value, present := block["Port"]; present {
		var valid bool
		port, valid = outlinePort(value)
		if !valid {
			return "", failure(FailureMalformedConfig, "Outline port must be an integer from 1 to 65535")
		}
	} else if websocket {
		port = "443"
	} else {
		return "", failure(FailureMalformedConfig, "Outline profile requires a port")
	}
	serverPort := net.JoinHostPort(host, port)
	encoded := base64.StdEncoding.EncodeToString([]byte(method + ":" + password))
	ssURL := "ss://" + encoded + "@" + serverPort
	if prefix := rawStringValue(block, "DisguisePrefix"); prefix != "" {
		ssURL += "?prefix=" + url.QueryEscape(prefix)
	}
	if !websocket {
		return ssURL, nil
	}
	path, ok := block["WebSocketPath"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", failure(FailureMalformedConfig, "Outline WebSocketPath must be a nonempty string")
	}
	base := strings.TrimRight(path, "/")
	if base != "" && !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	params := url.Values{"tcp_path": {base + "/tcp"}, "udp_path": {base + "/udp"}}
	return "tls:sni=" + host + "|ws:" + params.Encode() + "|" + ssURL, nil
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
func rawStringValue(block map[string]interface{}, key string) string {
	value, _ := block[key].(string)
	return value
}
