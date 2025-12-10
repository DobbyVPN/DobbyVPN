package configutil

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NormalizeTransportConfig normalizes the transport config to the outline-sdk format.
// Supports:
//   - ss:// URI with outline=1 for websocket mode
//   - ssconf:// URI for dynamic access keys
//   - Already formatted multi-part configs (with |)
func NormalizeTransportConfig(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("config is required")
	}

	// Already in multi-part format
	if strings.Contains(raw, "|") {
		return raw, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("failed to parse config: %w", err)
	}

	// Handle ssconf:// dynamic access key URL
	if strings.EqualFold(u.Scheme, "ssconf") {
		return fetchDynamicAccessKey(raw)
	}

	// Only process ss:// scheme
	if !strings.EqualFold(u.Scheme, "ss") {
		return raw, nil
	}

	query := u.Query()

	// Check if this is an outline websocket config
	if query.Get("outline") != "1" {
		return raw, nil
	}

	// Get the websocket path (just the path, not query params)
	wsPath := u.EscapedPath()
	if wsPath == "" {
		wsPath = "/"
	}

	// Build the shadowsocks URL with only the prefix parameter (if present)
	ssQuery := url.Values{}
	if prefix := query.Get("prefix"); prefix != "" {
		ssQuery.Set("prefix", prefix)
	}

	// Reconstruct the ss:// URL without outline parameter
	ssURL := &url.URL{
		Scheme:   "ss",
		User:     u.User,
		Host:     u.Host,
		RawQuery: ssQuery.Encode(),
	}

	var parts []string

	// Add TLS layer with SNI
	host := u.Hostname()
	if host != "" {
		tlsValues := url.Values{}
		tlsValues.Set("sni", host)
		tlsValues.Set("certname", host)
		parts = append(parts, "tls:"+tlsValues.Encode())
	}

	// Add WebSocket layer with paths
	wsValues := url.Values{}
	wsValues.Set("tcp_path", wsPath)
	wsValues.Set("udp_path", wsPath)
	parts = append(parts, "ws:"+wsValues.Encode())

	// Add shadowsocks config
	parts = append(parts, ssURL.String())

	return strings.Join(parts, "|"), nil
}

// fetchDynamicAccessKey fetches a dynamic access key from ssconf:// URL
func fetchDynamicAccessKey(ssconfURL string) (string, error) {
	// Convert ssconf:// to https://
	httpsURL := strings.Replace(ssconfURL, "ssconf://", "https://", 1)

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	resp, err := client.Get(httpsURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch dynamic access key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch dynamic access key: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read dynamic access key response: %w", err)
	}

	// The response should be a ss:// or transport config
	config := strings.TrimSpace(string(body))
	if config == "" {
		return "", errors.New("empty dynamic access key response")
	}

	// Recursively normalize the fetched config
	return NormalizeTransportConfig(config)
}

// ExtractShadowsocksHost extracts the shadowsocks server host from a config string.
// Works with both multi-part configs and simple ss:// URIs.
func ExtractShadowsocksHost(config string) (string, error) {
	config = strings.TrimSpace(config)
	if config == "" {
		return "", errors.New("config is required")
	}

	parts := strings.Split(config, "|")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		u, err := url.Parse(part)
		if err != nil {
			return "", fmt.Errorf("failed to parse config part: %w", err)
		}
		if strings.EqualFold(u.Scheme, "ss") {
			host := u.Hostname()
			if host == "" {
				return "", errors.New("shadowsocks host not specified")
			}
			return host, nil
		}
	}
	return "", errors.New("shadowsocks config not found")
}

// ParseShadowsocksURI parses an ss:// URI and returns its components.
// Format: ss://BASE64(method:password)@host:port[/path][?query]
// or:     ss://method:password@host:port[/path][?query]
func ParseShadowsocksURI(ssURI string) (method, password, host string, port int, err error) {
	u, err := url.Parse(ssURI)
	if err != nil {
		return "", "", "", 0, fmt.Errorf("failed to parse ss:// URI: %w", err)
	}

	if !strings.EqualFold(u.Scheme, "ss") {
		return "", "", "", 0, errors.New("not a ss:// URI")
	}

	host = u.Hostname()
	if host == "" {
		return "", "", "", 0, errors.New("host is required")
	}

	portStr := u.Port()
	if portStr == "" {
		return "", "", "", 0, errors.New("port is required")
	}
	fmt.Sscanf(portStr, "%d", &port)

	// Try to get credentials from userinfo
	if u.User != nil {
		userInfo := u.User.String()
		// Check if it's base64 encoded (method:password)
		if decoded, decErr := base64.URLEncoding.DecodeString(userInfo); decErr == nil {
			parts := strings.SplitN(string(decoded), ":", 2)
			if len(parts) == 2 {
				method = parts[0]
				password = parts[1]
				return
			}
		}
		// Try standard base64
		if decoded, decErr := base64.StdEncoding.DecodeString(userInfo); decErr == nil {
			parts := strings.SplitN(string(decoded), ":", 2)
			if len(parts) == 2 {
				method = parts[0]
				password = parts[1]
				return
			}
		}
		// Not base64, try as plain text
		if pwd, ok := u.User.Password(); ok {
			method = u.User.Username()
			password = pwd
			return
		}
	}

	return "", "", "", 0, errors.New("could not parse credentials from ss:// URI")
}
