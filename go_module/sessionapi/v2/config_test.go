package v2

import (
	"strings"
	"testing"
)

func TestParseConfigRejectsUnsupportedRootKeys(t *testing.T) {
	tests := map[string]string{
		"array table":           "[[Cloak]]\nname = 'legacy'\n",
		"ordinary table":        "[WireGuard]\nprivate_key = 'synthetic'\n",
		"root scalar":           "Cloak = true\n",
		"empty array table":     "[[Cloak]]\n",
		"empty ordinary table":  "[WireGuard]\n",
		"empty telemetry table": "[Telemetry]\n",
		"telemetry array table": "[[Telemetry]]\nmode = 'off'\n",
	}
	for name, unsupported := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfig([]byte(unsupported)); CodeOf(err) != FailureUnsupported {
				t.Fatalf("unsupported-only parseConfig error = %v", err)
			}
			for _, raw := range []string{
				unsupported + outlineConfig,
				outlineConfig + unsupported,
			} {
				if _, err := parseConfig([]byte(raw)); CodeOf(err) != FailureUnsupported {
					t.Fatalf("parseConfig error = %v", err)
				}
			}
		})
	}
}

func TestParseConfigKeepsSupportedRootAndProtocolPayloadsOpen(t *testing.T) {
	raw := "[ExcludeIPs]\nIPs = ['192.0.2.0/24']\n\n" +
		"[[Xray]]\noutbounds = [{ address = 'vpn.invalid', nested = { arbitrary = true } }]\n" +
		"[[TrustTunnel]]\n" +
		"[TrustTunnel.endpoint]\nhostname = 'vpn.invalid'\ncustom_protocol_option = 'accepted'\n"
	parsed, err := parseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("parseConfig rejected supported nested payload: %v", err)
	}
	if len(parsed.profiles) != 2 {
		t.Fatalf("profile count = %d, want 2", len(parsed.profiles))
	}
}

func TestParseConfigRejectsUnknownExcludeIPsSettings(t *testing.T) {
	raw := outlineConfig + "\n[ExcludeIPs]\nUnexpected = ['192.0.2.0/24']\n"
	if _, err := parseConfig([]byte(raw)); CodeOf(err) != FailureUnsupported {
		t.Fatalf("unknown ExcludeIPs setting error = %v", err)
	}
}

func TestParseConfigTrustTunnelRequiresCertificateVerification(t *testing.T) {
	base := "[[TrustTunnel]]\nname = 'synthetic'\n"
	tests := []struct {
		name     string
		field    string
		wantCode FailureCode
	}{
		{name: "absent defaults to enabled", wantCode: ""},
		{name: "false accepted", field: "[TrustTunnel.endpoint]\nskip_verification = false\n", wantCode: ""},
		{name: "true rejected", field: "[TrustTunnel.endpoint]\nskip_verification = true\n", wantCode: FailureMalformedConfig},
		{name: "wrong type rejected", field: "[TrustTunnel.endpoint]\nskip_verification = 'false'\n", wantCode: FailureMalformedConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig([]byte(base + tt.field))
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("parseConfig error = %v", err)
				}
			} else if CodeOf(err) != tt.wantCode {
				t.Fatalf("parseConfig error = %v, want %s", err, tt.wantCode)
			}
		})
	}
}

func TestParseConfigRejectsMalformedCloakValues(t *testing.T) {
	for name, value := range map[string]string{
		"string true": "'true'",
		"number":      "1",
	} {
		t.Run(name, func(t *testing.T) {
			raw := "[[Outline]]\nCloak = " + value + "\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n"
			if _, err := parseConfig([]byte(raw)); CodeOf(err) != FailureMalformedConfig {
				t.Fatalf("parseConfig error = %v, want malformed config", err)
			}
		})
	}
}

func TestParseConfigRejectsOversizedInputBeforeDecode(t *testing.T) {
	raw := []byte(outlineConfig + "#" + strings.Repeat("x", maxConfigBytes))
	if _, err := parseConfig(raw); CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("oversized parse error = %v", err)
	}
}

const outlineConfig = "[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"
