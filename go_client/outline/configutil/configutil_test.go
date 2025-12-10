package configutil

import (
	"strings"
	"testing"
)

func TestNormalizeTransportConfigOutlineShareKey(t *testing.T) {
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/?outline=1"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	expected := "tls:certname=example.com&sni=example.com|ws:tcp_path=%2F&udp_path=%2F|ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443"
	if normalized != expected {
		t.Fatalf("unexpected normalized config.\nwant: %s\n got: %s", expected, normalized)
	}
}

func TestNormalizeTransportConfigOutlineShareKeyWithPrefix(t *testing.T) {
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/?outline=1&prefix=HTTP%2F1.1%20"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	// prefix should stay on ss:// URL, not in ws path
	expected := "tls:certname=example.com&sni=example.com|ws:tcp_path=%2F&udp_path=%2F|ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443?prefix=HTTP%2F1.1+"
	if normalized != expected {
		t.Fatalf("unexpected normalized config.\nwant: %s\n got: %s", expected, normalized)
	}
}

func TestNormalizeTransportConfigOutlineWithCustomPath(t *testing.T) {
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/custom/path?outline=1"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	expected := "tls:certname=example.com&sni=example.com|ws:tcp_path=%2Fcustom%2Fpath&udp_path=%2Fcustom%2Fpath|ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443"
	if normalized != expected {
		t.Fatalf("unexpected normalized config.\nwant: %s\n got: %s", expected, normalized)
	}
}

func TestNormalizeTransportConfigOutlineWithCustomPathAndPrefix(t *testing.T) {
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/ws-path?outline=1&prefix=%16%03%01"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	// ws path should be /ws-path, prefix should stay on ss:// URL
	expected := "tls:certname=example.com&sni=example.com|ws:tcp_path=%2Fws-path&udp_path=%2Fws-path|ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443?prefix=%16%03%01"
	if normalized != expected {
		t.Fatalf("unexpected normalized config.\nwant: %s\n got: %s", expected, normalized)
	}
}

func TestNormalizeTransportConfigAlreadyMultiPart(t *testing.T) {
	raw := "tls:sni=example.com|ss://foo"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	if normalized != raw {
		t.Fatalf("expected config to remain unchanged, got %s", normalized)
	}
}

func TestNormalizeTransportConfigPlainShadowsocks(t *testing.T) {
	// Plain ss:// without outline=1 should remain unchanged
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	if normalized != raw {
		t.Fatalf("expected config to remain unchanged, got %s", normalized)
	}
}

func TestNormalizeTransportConfigPlainShadowsocksWithPrefix(t *testing.T) {
	// Plain ss:// with prefix but without outline=1 should remain unchanged
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443?prefix=HTTP%2F1.1+"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	if normalized != raw {
		t.Fatalf("expected config to remain unchanged, got %s", normalized)
	}
}

func TestExtractShadowsocksHost(t *testing.T) {
	cfg := "tls:sni=example.com|ws:tcp_path=%2Ffoo&udp_path=%2Ffoo|ss://YWVzOnBhc3M=@example.com:1234"
	host, err := ExtractShadowsocksHost(cfg)
	if err != nil {
		t.Fatalf("ExtractShadowsocksHost returned error: %v", err)
	}
	if host != "example.com" {
		t.Fatalf("expected host example.com, got %s", host)
	}
}

func TestExtractShadowsocksHostSimple(t *testing.T) {
	cfg := "ss://YWVzOnBhc3M=@myserver.net:8388"
	host, err := ExtractShadowsocksHost(cfg)
	if err != nil {
		t.Fatalf("ExtractShadowsocksHost returned error: %v", err)
	}
	if host != "myserver.net" {
		t.Fatalf("expected host myserver.net, got %s", host)
	}
}

func TestParseShadowsocksURI(t *testing.T) {
	// Test base64 encoded credentials
	uri := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443"
	method, password, host, port, err := ParseShadowsocksURI(uri)
	if err != nil {
		t.Fatalf("ParseShadowsocksURI returned error: %v", err)
	}
	if method != "aes-256-gcm" {
		t.Errorf("expected method aes-256-gcm, got %s", method)
	}
	if password != "password" {
		t.Errorf("expected password 'password', got %s", password)
	}
	if host != "example.com" {
		t.Errorf("expected host example.com, got %s", host)
	}
	if port != 443 {
		t.Errorf("expected port 443, got %d", port)
	}
}

// TestNormalizeTransportConfigPlainShadowsocksWithBinaryPrefix tests binary prefix
// that mimics TLS ClientHello (for DPI evasion)
func TestNormalizeTransportConfigPlainShadowsocksWithBinaryPrefix(t *testing.T) {
	// Binary prefix that looks like TLS ClientHello: \x16\x03\x01\x00\xa8\x01\x01
	// URL-encoded as: %16%03%01%00%A8%01%01
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443?prefix=%16%03%01%00%A8%01%01"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	// Plain SS with prefix should remain unchanged
	if normalized != raw {
		t.Fatalf("expected config to remain unchanged, got %s", normalized)
	}
}

// TestNormalizeTransportConfigWebSocketWithBinaryPrefix tests SS over WebSocket with binary prefix
func TestNormalizeTransportConfigWebSocketWithBinaryPrefix(t *testing.T) {
	// SS over WebSocket with TLS-like prefix
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/?outline=1&prefix=%16%03%01"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}

	// Should have tls, ws, and ss parts
	if !strings.Contains(normalized, "tls:") {
		t.Error("expected tls: in normalized config")
	}
	if !strings.Contains(normalized, "ws:") {
		t.Error("expected ws: in normalized config")
	}
	if !strings.Contains(normalized, "ss://") {
		t.Error("expected ss:// in normalized config")
	}
	// Prefix should be in ss:// part, not in ws: part
	if !strings.Contains(normalized, "ss://") || !strings.HasSuffix(normalized, "prefix=%16%03%01") {
		t.Errorf("prefix should be at the end of ss:// URL, got: %s", normalized)
	}
}

// TestSupportedConfigurations documents all supported configuration formats
func TestSupportedConfigurations(t *testing.T) {
	testCases := []struct {
		name        string
		input       string
		shouldParse bool
		description string
	}{
		{
			name:        "Plain Shadowsocks",
			input:       "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:8388",
			shouldParse: true,
			description: "Basic Shadowsocks without any additional layers",
		},
		{
			name:        "Shadowsocks with HTTP prefix",
			input:       "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:8388?prefix=HTTP%2F1.1+",
			shouldParse: true,
			description: "Shadowsocks with HTTP-like prefix for DPI evasion",
		},
		{
			name:        "Shadowsocks with TLS prefix",
			input:       "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443?prefix=%16%03%01",
			shouldParse: true,
			description: "Shadowsocks with TLS ClientHello-like prefix",
		},
		{
			name:        "Shadowsocks over WebSocket (Outline)",
			input:       "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/?outline=1",
			shouldParse: true,
			description: "Shadowsocks tunneled through WebSocket with TLS",
		},
		{
			name:        "Shadowsocks over WebSocket with prefix",
			input:       "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/?outline=1&prefix=%16%03%01",
			shouldParse: true,
			description: "Full setup: TLS + WebSocket + Shadowsocks with prefix",
		},
		{
			name:        "Shadowsocks over WebSocket with custom path",
			input:       "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/secret-path?outline=1",
			shouldParse: true,
			description: "Shadowsocks over WebSocket with custom WebSocket path",
		},
		{
			name:        "Pre-formatted multi-part config",
			input:       "tls:sni=example.com|ws:tcp_path=/foo|ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443",
			shouldParse: true,
			description: "Already formatted config passes through unchanged",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := NormalizeTransportConfig(tc.input)
			if tc.shouldParse {
				if err != nil {
					t.Fatalf("Failed to parse %s: %v", tc.description, err)
				}
				if normalized == "" {
					t.Fatalf("Empty result for %s", tc.description)
				}
				t.Logf("✓ %s\n  Input:  %s\n  Output: %s", tc.description, tc.input, normalized)
			} else {
				if err == nil {
					t.Fatalf("Expected error for %s, got: %s", tc.description, normalized)
				}
			}
		})
	}
}
