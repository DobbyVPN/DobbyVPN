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
		return configRoot{}, nil, failure(FailureMalformedConfig, "The subscription is empty. Expected TOML with at least one [[Outline]], [[Xray]] or [[TrustTunnel]] profile, see "+subscriptionFormatURL)
	}
	var root configRoot
	metadata, err := toml.Decode(string(raw), &root)
	if err != nil {
		if what, foreign := detectForeignFormat(raw); foreign {
			return configRoot{}, nil, notDobbySubscription(what)
		}
		return configRoot{}, nil, failureWithCause(FailureMalformedConfig, "This is not a valid DobbyVPN subscription: TOML could not be parsed", withFormatPointer{err})
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
	if value, present := block["Description"]; present {
		var ok bool
		description, ok = value.(string)
		if !ok {
			return RuntimeProfile{}, ref, nil, ref.reject(invalidField("Description", "must be a string"))
		}
	}
	ref.description = description
	if cloakValue, present := block["Cloak"]; present {
		cloak, ok := cloakValue.(bool)
		if !ok {
			return RuntimeProfile{}, ref, nil, ref.reject(invalidField("Cloak", "must be true or false"))
		}
		if cloak {
			return RuntimeProfile{}, ref, nil, ref.reject(&fieldError{code: FailureUnsupported, field: "Cloak", reason: "removed Cloak profiles are not supported"})
		}
	}
	if protocol == ProtocolTrustTunnel {
		if err := validateTrustTunnelVerification(block); err != nil {
			return RuntimeProfile{}, ref, nil, ref.reject(err)
		}
	}
	normalized, warnings, err := normalizeProfileChecked(protocol, block)
	if err != nil {
		return RuntimeProfile{}, ref, nil, ref.reject(err)
	}
	return RuntimeProfile{
		Summary:      ProfileSummary{Index: profileIndex, Protocol: protocol, Description: description},
		Config:       normalized,
		ExcludeCIDRs: append([]string(nil), root.ExcludeIPs.IPs...),
	}, ref, warnings, nil
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
		return invalidField("skip_verification", "is only supported under endpoint")
	}
	endpointValue, hasEndpoint := block["endpoint"]
	if !hasEndpoint {
		return nil
	}
	endpoint, ok := endpointValue.(map[string]interface{})
	if !ok {
		return invalidField("endpoint", "must be a table")
	}
	value, present := endpoint["skip_verification"]
	if !present {
		return nil
	}
	skipVerification, ok := value.(bool)
	if !ok {
		return invalidField("endpoint.skip_verification", "must be true or false")
	}
	if skipVerification {
		return invalidField("endpoint.skip_verification", "certificate verification must remain enabled")
	}
	return nil
}

func normalizeProfile(kind Protocol, block map[string]any) (protocolconfig.Config, error) {
	normalized, _, err := normalizeProfileChecked(kind, block)
	return normalized, err
}

func normalizeProfileChecked(kind Protocol, block map[string]any) (protocolconfig.Config, []profileWarning, error) {
	switch kind {
	case ProtocolXray:
		outbounds, ok := block["outbounds"]
		if !ok {
			return protocolconfig.Config{}, nil, invalidField("outbounds", "is required")
		}
		switch outbounds.(type) {
		case []any, []map[string]any:
		default:
			return protocolconfig.Config{}, nil, invalidField("outbounds", "must be an array")
		}
		return protocolconfig.Config{Xray: protocolconfig.CloneFields(block)}, nil, nil
	case ProtocolOutline:
		normalized, warnings, err := normalizeOutlineBlock(block)
		return protocolconfig.Config{OutlineURL: normalized}, warnings, err
	case ProtocolTrustTunnel:
		return protocolconfig.Config{TrustTunnel: protocolconfig.CloneFields(block)}, nil, nil
	default:
		return protocolconfig.Config{}, nil, failure(FailureUnsupported, "unsupported protocol")
	}
}

// normalizeOutlineURL is the Go equivalent of the legacy shell builder. It is
// intentionally kept here, before any platform binding sees configuration, so
// Android/iOS/desktop cannot diverge in method defaults, websocket paths, or
// transport URL escaping. Protocol validation remains the runtime's job.
func normalizeOutlineURL(block map[string]interface{}) (string, error) {
	normalized, _, err := normalizeOutlineBlock(block)
	return normalized, err
}

// normalizeOutlineBlock also reports settings that are accepted but have no
// effect (unknown keys, an ignored WebSocketPath or DisguisePrefix). Those stay
// warnings so that subscriptions that connect today keep connecting.
func normalizeOutlineBlock(block map[string]interface{}) (string, []profileWarning, error) {
	warnings := unknownOutlineFieldWarnings(block)
	method := "chacha20-ietf-poly1305"
	if value, present := block["Method"]; present {
		text, ok := value.(string)
		if !ok {
			return "", nil, invalidField("Method", "must be a string")
		}
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			method = trimmed
		}
	}
	password := ""
	if value, present := block["Password"]; present {
		text, ok := value.(string)
		if !ok {
			return "", nil, invalidField("Password", "must be a string")
		}
		password = strings.TrimSpace(text)
	}
	if password == "" {
		return "", nil, invalidField("Password", "is required")
	}
	server := stringValue(block, "Server")
	if server == "" {
		return "", nil, invalidField("Server", "is required")
	}
	host, ok := outlineServerHost(server)
	if !ok {
		return "", nil, invalidField("Server", "must be a hostname or IP address")
	}
	websocket := false
	if value, present := block["WebSocket"]; present {
		var valid bool
		websocket, valid = value.(bool)
		if !valid {
			return "", nil, invalidField("WebSocket", "must be true or false")
		}
	}
	port := ""
	if value, present := block["Port"]; present {
		var valid bool
		port, valid = outlinePort(value)
		if !valid {
			return "", nil, invalidField("Port", "must be an integer from 1 to 65535")
		}
	} else if websocket {
		port = "443"
	} else {
		return "", nil, invalidField("Port", "is required unless WebSocket = true")
	}
	serverPort := net.JoinHostPort(host, port)
	encoded := base64.StdEncoding.EncodeToString([]byte(method + ":" + password))
	ssURL := "ss://" + encoded + "@" + serverPort
	if value, present := block["DisguisePrefix"]; present {
		if prefix, isString := value.(string); !isString {
			warnings = append(warnings, profileWarning{field: "DisguisePrefix", message: `field "DisguisePrefix" must be a string, ignoring it`})
		} else if prefix != "" {
			ssURL += "?prefix=" + url.QueryEscape(prefix)
		}
	}
	if !websocket {
		if _, present := block["WebSocketPath"]; present {
			warnings = append(warnings, profileWarning{field: "WebSocketPath", message: `field "WebSocketPath" is ignored because WebSocket is not true`})
		}
		return ssURL, warnings, nil
	}
	path, ok := block["WebSocketPath"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", nil, invalidField("WebSocketPath", "must be a non-empty string when WebSocket = true")
	}
	base := strings.TrimRight(path, "/")
	if base != "" && !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	params := url.Values{"tcp_path": {base + "/tcp"}, "udp_path": {base + "/udp"}}
	return "tls:sni=" + host + "|ws:" + params.Encode() + "|" + ssURL, warnings, nil
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
