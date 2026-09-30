package sessionapi

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
			combined := []string{unsupported + outlineConfig}
			if name != "root scalar" {
				combined = append(combined, outlineConfig+unsupported)
			}
			for _, raw := range combined {
				if _, err := parseConfig([]byte(raw)); CodeOf(err) != FailureUnsupported {
					t.Fatalf("parseConfig error = %v", err)
				}
			}
		})
	}
}

func TestParseConfigKeepsProtocolPayloadsOpen(t *testing.T) {
	raw := "[ExcludeIPs]\nIPs = ['192.0.2.0/24']\n\n" +
		"[[Xray]]\noutbounds = [{ address = 'vpn.invalid', nested = { arbitrary = true } }]\n" +
		"[[TrustTunnel]]\n[TrustTunnel.endpoint]\n" +
		"hostname = 'vpn.invalid'\ncustom_protocol_option = 'accepted'\n"
	parsed, err := parseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("parseConfig rejected supported nested payload: %v", err)
	}
	if len(parsed.profiles) != 2 {
		t.Fatalf("profile count = %d, want 2", len(parsed.profiles))
	}
	if parsed.profiles[0].Summary.Protocol != ProtocolXray || parsed.profiles[1].Summary.Protocol != ProtocolTrustTunnel {
		t.Fatalf("profile order = %#v", parsed.profiles)
	}
	if got := parsed.profiles[0].ExcludeCIDRs; len(got) != 1 || got[0] != "192.0.2.0/24" {
		t.Fatalf("exclude IPs = %#v", got)
	}
}

func TestParseConfigUsesOrderedArrayTableMetadata(t *testing.T) {
	raw := `[[Outline]]
Description = """
This multiline description contains a literal header:
[[Outline]]
It is still description text.
"""
Server = 'outline.invalid'
Port = 443
Password = 'synthetic'

[[Xray]]
Description = 'Xray profile'
outbounds = [{ address = 'xray.invalid' }]

[[TrustTunnel]]
Description = 'TrustTunnel profile'

[[Outline]]
Description = 'second Outline profile'
Server = 'outline-two.invalid'
Port = 443
Password = 'synthetic'

[ExcludeIPs]
IPs = ['192.0.2.0/24']
`
	parsed, err := parseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("parseConfig rejected ordered array tables: %v", err)
	}
	want := []struct {
		protocol    Protocol
		description string
	}{
		{ProtocolOutline, "This multiline description contains a literal header:\n[[Outline]]\nIt is still description text.\n"},
		{ProtocolXray, "Xray profile"},
		{ProtocolTrustTunnel, "TrustTunnel profile"},
		{ProtocolOutline, "second Outline profile"},
	}
	if len(parsed.profiles) != len(want) {
		t.Fatalf("profile count = %d, want %d", len(parsed.profiles), len(want))
	}
	for i, expected := range want {
		got := parsed.profiles[i].Summary
		if got.Index != int32(i) || got.Protocol != expected.protocol || got.Description != expected.description {
			t.Errorf("profile %d = %#v, want protocol %s and description %q", i, got, expected.protocol, expected.description)
		}
		if len(parsed.profiles[i].ExcludeCIDRs) != 1 || parsed.profiles[i].ExcludeCIDRs[0] != "192.0.2.0/24" {
			t.Errorf("profile %d exclude IPs = %#v", i, parsed.profiles[i].ExcludeCIDRs)
		}
	}
}

func TestParseConfigRejectsUnknownRootSettings(t *testing.T) {
	raw := "unexpected = ['198.51.100.0/24']\n" + outlineProfile
	if _, err := parseConfig([]byte(raw)); CodeOf(err) != FailureUnsupported {
		t.Fatalf("unknown root setting error = %v", err)
	}
}

func TestParseConfigRejectsVersionedProfilesEnvelope(t *testing.T) {
	raw := "schema_version = 2\n[[profiles]]\nprotocol = 'OUTLINE'\n" +
		"[profiles.config]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n"
	if _, err := parseConfig([]byte(raw)); CodeOf(err) != FailureUnsupported {
		t.Fatalf("versioned profiles envelope error = %v, want unsupported", err)
	}
}

func TestParseConfigTrustTunnelRequiresCertificateVerification(t *testing.T) {
	base := "[[TrustTunnel]]\nDescription = 'synthetic'\n"
	tests := []struct {
		name     string
		field    string
		wantCode FailureCode
	}{
		{name: "absent defaults to enabled", wantCode: ""},
		{name: "false accepted", field: "[TrustTunnel.endpoint]\nskip_verification = false\n", wantCode: ""},
		{name: "true rejected", field: "[TrustTunnel.endpoint]\nskip_verification = true\n", wantCode: FailureMalformedConfig},
		{name: "wrong type rejected", field: "[TrustTunnel.endpoint]\nskip_verification = 'false'\n", wantCode: FailureMalformedConfig},
		{name: "root true rejected", field: "skip_verification = true\n", wantCode: FailureMalformedConfig},
		{name: "root false rejected", field: "skip_verification = false\n", wantCode: FailureMalformedConfig},
		{name: "root string rejected", field: "skip_verification = 'false'\n", wantCode: FailureMalformedConfig},
		{name: "root integer rejected", field: "skip_verification = 0\n", wantCode: FailureMalformedConfig},
		{name: "root false with endpoint true rejected", field: "skip_verification = false\n[TrustTunnel.endpoint]\nskip_verification = true\n", wantCode: FailureMalformedConfig},
		{name: "root true with endpoint false rejected", field: "skip_verification = true\n[TrustTunnel.endpoint]\nskip_verification = false\n", wantCode: FailureMalformedConfig},
		{name: "inline endpoint true rejected", field: "endpoint = { skip_verification = true }\n", wantCode: FailureMalformedConfig},
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

func TestParseConfigRejectsMisplacedTrustTunnelVerificationWithOtherProfiles(t *testing.T) {
	trustTunnel := "[[TrustTunnel]]\nskip_verification = false\n"
	for name, raw := range map[string]string{
		"outline first":     outlineConfig + trustTunnel,
		"trusttunnel first": trustTunnel + outlineProfile,
	} {
		t.Run(name, func(t *testing.T) {
			parsed, err := parseConfig([]byte(raw))
			if CodeOf(err) != FailureMalformedConfig {
				t.Fatalf("parseConfig error = %v, want malformed config", err)
			}
			if parsed.profiles != nil {
				t.Fatalf("parseConfig returned usable profiles after rejection: %#v", parsed.profiles)
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

func TestParseConfigRejectsNonStringDescription(t *testing.T) {
	raw := "[[Outline]]\nDescription = 7\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n"
	if _, err := parseConfig([]byte(raw)); CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("non-string Description error = %v, want malformed config", err)
	}
}

func TestParseConfigRejectsOversizedInputBeforeDecode(t *testing.T) {
	raw := []byte(outlineConfig + "#" + strings.Repeat("x", maxConfigBytes))
	if _, err := parseConfig(raw); CodeOf(err) != FailureMalformedConfig {
		t.Fatalf("oversized parse error = %v", err)
	}
}

const outlineProfile = "[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"
const outlineConfig = outlineProfile
