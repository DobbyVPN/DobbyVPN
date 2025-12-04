package configutil

import "testing"

func TestNormalizeTransportConfigOutlineShareKey(t *testing.T) {
	raw := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443/?outline=1"
	normalized, err := NormalizeTransportConfig(raw)
	if err != nil {
		t.Fatalf("NormalizeTransportConfig returned error: %v", err)
	}
	expectedPrefix := "tls:certname=example.com&sni=example.com|ws:tcp_path=%2F%3Foutline%3D1&udp_path=%2F%3Foutline%3D1|ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=@example.com:443"
	if normalized != expectedPrefix {
		t.Fatalf("unexpected normalized config.\nwant: %s\n got: %s", expectedPrefix, normalized)
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
