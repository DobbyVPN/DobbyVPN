package sessionapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// The container owns profile ordering. Protocol payloads remain open and are
// validated by their existing protocol-specific normalizers and runtimes.
type configRoot struct {
	SchemaVersion int             `toml:"schema_version"`
	ExcludeIPs    []string        `toml:"exclude_ips"`
	Profiles      []configProfile `toml:"profiles"`
}

type configProfile struct {
	Protocol    Protocol               `toml:"protocol"`
	Description string                 `toml:"description"`
	Config      map[string]interface{} `toml:"config"`
}

type parsedConfig struct {
	digest   string
	profiles []RuntimeProfile
}

const maxConfigBytes = 1 << 20

func parseConfig(raw []byte) (parsedConfig, error) {
	root, err := decodeConfig(raw)
	if err != nil {
		return parsedConfig{}, err
	}
	profiles, err := parseProfiles(root)
	if err != nil {
		return parsedConfig{}, err
	}
	digest := sha256.Sum256(raw)
	return parsedConfig{digest: hex.EncodeToString(digest[:]), profiles: profiles}, nil
}

func decodeConfig(raw []byte) (configRoot, error) {
	if len(raw) > maxConfigBytes {
		return configRoot{}, failure(FailureMalformedConfig, "configuration exceeds the 1 MiB size limit")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return configRoot{}, failure(FailureMalformedConfig, "configuration is blank")
	}
	var root configRoot
	metadata, err := toml.Decode(string(raw), &root)
	if err != nil {
		return configRoot{}, failureWithCause(FailureMalformedConfig, "TOML could not be parsed", err)
	}
	if err := validateContainerKeys(metadata.Keys()); err != nil {
		return configRoot{}, err
	}
	if root.SchemaVersion != 2 {
		return configRoot{}, failure(FailureUnsupported, "configuration requires schema_version = 2")
	}
	if len(root.Profiles) == 0 {
		return configRoot{}, failure(FailureMalformedConfig, "configuration requires one or more [[profiles]] sections")
	}
	for _, key := range metadata.Keys() {
		if len(key) == 1 && key[0] == "profiles" && metadata.Type(key...) != "ArrayHash" {
			return configRoot{}, failure(FailureMalformedConfig, "profiles must be an array of tables")
		}
	}
	return root, nil
}

func parseProfiles(root configRoot) ([]RuntimeProfile, error) {
	profiles := make([]RuntimeProfile, 0, len(root.Profiles))
	for index, block := range root.Profiles {
		profile, err := parseProfile(root, block, int32(index))
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func parseProfile(root configRoot, profile configProfile, profileIndex int32) (RuntimeProfile, error) {
	block, protocol := profile.Config, profile.Protocol
	if protocol != ProtocolOutline && protocol != ProtocolXray && protocol != ProtocolTrustTunnel {
		return RuntimeProfile{}, failure(FailureUnsupported, "configuration contains an unsupported profile protocol")
	}
	if block == nil {
		return RuntimeProfile{}, failure(FailureMalformedConfig, "profile requires a config table")
	}
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
	var payload []byte
	if protocol == ProtocolTrustTunnel {
		var err error
		payload, err = encodeProfile(block)
		if err != nil {
			return RuntimeProfile{}, failureWithCause(FailureMalformedConfig, "a TrustTunnel profile could not be encoded", err)
		}
	}
	normalized, err := normalizeProfile(protocol, block, payload)
	if err != nil {
		return RuntimeProfile{}, err
	}
	return RuntimeProfile{
		Summary:          ProfileSummary{Index: profileIndex, Protocol: protocol, Description: profile.Description},
		NormalizedConfig: normalized,
		ExcludeCIDRs:     append([]string(nil), root.ExcludeIPs...),
	}, nil
}

func validateContainerKeys(keys []toml.Key) error {
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		switch key[0] {
		case "schema_version", "exclude_ips":
			if len(key) != 1 {
				return failure(FailureUnsupported, "configuration contains an unsupported container setting")
			}
		case "profiles":
			if len(key) > 1 && key[1] != "protocol" && key[1] != "description" && key[1] != "config" {
				return failure(FailureUnsupported, "profile contains an unsupported container setting")
			}
			if len(key) > 2 && key[1] != "config" {
				return failure(FailureUnsupported, "profile contains an unsupported container setting")
			}
		default:
			return failure(FailureUnsupported, "configuration contains an unsupported container setting")
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

func encodeProfile(block map[string]interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(block); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(buf.String()) + "\n"), nil
}

func normalizeProfile(protocol Protocol, block map[string]interface{}, raw []byte) ([]byte, error) {
	switch protocol {
	case ProtocolXray:
		if _, ok := block["outbounds"]; !ok {
			return nil, failure(FailureMalformedConfig, "Xray profile requires outbounds")
		}
		data, err := json.Marshal(block)
		return data, err
	case ProtocolOutline:
		normalized, err := normalizeOutlineURL(block)
		return []byte(normalized), err
	case ProtocolTrustTunnel:
		return append([]byte(nil), raw...), nil
	default:
		return nil, failure(FailureUnsupported, "unsupported protocol")
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
	port := intString(block["Port"])
	websocket := boolValue(block, "WebSocket")
	if server == "" {
		return "", failure(FailureMalformedConfig, "Outline profile requires a server")
	}
	if port == "" && websocket {
		port = "443"
	}
	if port == "" {
		return "", failure(FailureMalformedConfig, "Outline profile requires a port")
	}
	serverPort := server
	serverPort += ":" + port
	encoded := base64.StdEncoding.EncodeToString([]byte(method + ":" + password))
	ssURL := "ss://" + encoded + "@" + serverPort
	if prefix := rawStringValue(block, "DisguisePrefix"); prefix != "" {
		separator := "?"
		if strings.Contains(serverPort, "?") {
			separator = "&"
		}
		ssURL += separator + "prefix=" + url.QueryEscape(prefix)
	}
	if !websocket {
		return ssURL, nil
	}
	base := strings.TrimRight(stringValue(block, "WebSocketPath"), "/")
	params := make([]string, 0, 2)
	if base != "" {
		params = append(params, "tcp_path="+base+"/tcp", "udp_path="+base+"/udp")
	}
	return "tls:sni=" + outlineHost(serverPort) + "|ws:" + strings.Join(params, "&") + "|" + ssURL, nil
}

func stringValue(block map[string]interface{}, key string) string {
	value, _ := block[key].(string)
	return strings.TrimSpace(value)
}
func rawStringValue(block map[string]interface{}, key string) string {
	value, _ := block[key].(string)
	return value
}
func boolValue(block map[string]interface{}, key string) bool {
	value, _ := block[key].(bool)
	return value
}
func intString(value interface{}) string {
	switch v := value.(type) {
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}
func outlineHost(serverPort string) string {
	host := strings.Split(serverPort, "?")[0]
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 0 {
			return host[1:end]
		}
	}
	if strings.Count(host, ":") == 1 {
		return strings.Split(host, ":")[0]
	}
	return host
}
