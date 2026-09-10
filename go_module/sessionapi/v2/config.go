package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// This shape deliberately keeps Xray/Outline/TrustTunnel bodies open. The
// session API validates the container and ordering, while protocol runtimes
// remain responsible for their existing protocol-specific validation.
type configRoot struct {
	Telemetry   map[string]interface{}   `toml:"Telemetry"`
	ExcludeIPs  excludeIPsConfig         `toml:"ExcludeIPs"`
	Outline     []map[string]interface{} `toml:"Outline"`
	Xray        []map[string]interface{} `toml:"Xray"`
	TrustTunnel []map[string]interface{} `toml:"TrustTunnel"`
}

type excludeIPsConfig struct {
	IPs []string `toml:"IPs"`
}

type parsedConfig struct {
	digest   string
	profiles []RuntimeProfile
	warnings []Warning
}

var (
	protocolHeaderRE = regexp.MustCompile(`(?m)^\s*\[\[\s*(Outline|Xray|TrustTunnel)\s*]]`)
)

// InspectProfiles validates raw configuration with the product parser and
// returns the connection inventory. It performs no network or session
// operation.
func InspectProfiles(raw []byte) ([]ProfileSummary, error) {
	parsed, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}
	return summaries(parsed.profiles), nil
}

func parseConfig(raw []byte) (parsedConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return parsedConfig{}, failure(FailureMalformedConfig, "configuration is blank")
	}
	text := string(raw)
	if !protocolHeaderRE.MatchString(text) {
		return parsedConfig{}, failure(FailureMalformedConfig, "expected one or more [[Outline]], [[Xray]], or [[TrustTunnel]] sections")
	}
	var root configRoot
	if _, err := toml.Decode(text, &root); err != nil {
		return parsedConfig{}, failure(FailureMalformedConfig, "TOML could not be parsed")
	}
	if root.Telemetry != nil {
		return parsedConfig{}, failure(FailureUnsupported, "configuration contains removed Telemetry settings")
	}

	headers := protocolHeaderRE.FindAllStringSubmatch(text, -1)
	counts := map[string]int{}
	for _, header := range headers {
		counts[header[1]]++
	}
	if counts["Outline"] != len(root.Outline) || counts["Xray"] != len(root.Xray) || counts["TrustTunnel"] != len(root.TrustTunnel) {
		return parsedConfig{}, failure(FailureMalformedConfig, "protocol section count does not match TOML data")
	}

	next := map[string]int{}
	profiles := make([]RuntimeProfile, 0, len(headers))
	for _, header := range headers {
		name := header[1]
		var block map[string]interface{}
		var protocol Protocol
		switch name {
		case "Outline":
			block, protocol = root.Outline[next[name]], ProtocolOutline
		case "Xray":
			block, protocol = root.Xray[next[name]], ProtocolXray
		case "TrustTunnel":
			block, protocol = root.TrustTunnel[next[name]], ProtocolTrustTunnel
		}
		next[name]++
		if boolValue(block, "Cloak") {
			return parsedConfig{}, failure(FailureUnsupported, "configuration contains a removed Cloak profile")
		}
		payload, err := encodeProfile(block)
		if err != nil {
			return parsedConfig{}, failure(FailureMalformedConfig, "a protocol profile could not be encoded")
		}
		description, _ := block["Description"].(string)
		normalized, format, err := normalizeProfile(protocol, block, payload)
		if err != nil {
			return parsedConfig{}, err
		}
		profiles = append(profiles, RuntimeProfile{
			Summary:          ProfileSummary{Index: len(profiles), Protocol: protocol, Description: description},
			NormalizedFormat: format,
			NormalizedConfig: normalized,
			ExcludeCIDRs:     append([]string(nil), root.ExcludeIPs.IPs...),
		})
	}
	if len(profiles) == 0 {
		return parsedConfig{}, failure(FailureMalformedConfig, "configuration contains no protocol profiles")
	}
	digest := sha256.Sum256(raw)
	return parsedConfig{digest: hex.EncodeToString(digest[:]), profiles: profiles}, nil
}

func encodeProfile(block map[string]interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(block); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(buf.String()) + "\n"), nil
}

func normalizeProfile(protocol Protocol, block map[string]interface{}, raw []byte) ([]byte, ConfigFormat, error) {
	switch protocol {
	case ProtocolXray:
		if _, ok := block["outbounds"]; !ok {
			return nil, "", failure(FailureMalformedConfig, "Xray profile requires outbounds")
		}
		data, err := json.Marshal(block)
		return data, ConfigJSON, err
	case ProtocolOutline:
		normalized, err := normalizeOutlineURL(block)
		return []byte(normalized), ConfigTransportURL, err
	case ProtocolTrustTunnel:
		return append([]byte(nil), raw...), ConfigTOML, nil
	default:
		return nil, "", failure(FailureUnsupported, "unsupported protocol")
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
