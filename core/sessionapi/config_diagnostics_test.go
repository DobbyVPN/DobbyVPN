package sessionapi

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"core/log"
)

const formatURL = "https://github.com/DobbyVPN/DobbyVPN#using-dobbyvpn"

const diagnosticsOutlineProfile = "[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n\n"

func b64(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }

func TestMixedExcludePolicyHash(t *testing.T) {
	const entries = `[ExcludeIPs]
IPs = ["2001:db8::/64", "::ffff:192.0.2.1", "ffff:ffff:ffff:ffff::/64", "192.0.2.2"]
`
	parsed, err := parseConfig([]byte(entries + diagnosticsOutlineProfile))
	if err != nil {
		t.Fatal(err)
	}
	const want = "b8915d077d5b7affb40bce6cbf82b4ecc5507c7c7708c951bb6a70af9da077df"
	if got := parsed.profiles[0].ExcludeSet.Hash(); got != want {
		t.Fatalf("excluded IP policy hash = %s, want %s", got, want)
	}
}

func TestParseConfigRecognisesForeignFormats(t *testing.T) {
	links := "vless://00000000-0000-0000-0000-000000000000@host.invalid:443?type=tcp#one\n" +
		"trojan://synthetic@host.invalid:443#two\n"
	// ">>>" and "???" encode to '+' and '/' in the standard alphabet, so these
	// bodies really differ from the standard encoding; the length is not a
	// multiple of three, so the raw form has no padding.
	urlLinks := links + "trojan://synthetic@host.invalid:443/?a=???>>>#x\n"
	urlSafe := base64.URLEncoding.EncodeToString([]byte(urlLinks))
	urlSafeRaw := base64.RawURLEncoding.EncodeToString([]byte(urlLinks))
	if !strings.ContainsAny(urlSafe, "-_") || !strings.Contains(urlSafe, "=") || strings.Contains(urlSafeRaw, "=") {
		t.Fatalf("fixture does not exercise the URL alphabet: %q", urlSafe)
	}
	tests := []struct{ name, body, what string }{
		{"html with a profile header inside", "<html><body><pre>\n[[Outline]]\n</pre></body></html>", "an HTML page"},
		{"yaml with a profile header inside a value", "notes: 'see [[Outline]] below'\nmode: rule\n", "YAML"},
		{"html doctype", "<!DOCTYPE html><html><body>Sign in</body></html>", "an HTML page"},
		{"html fragment", "  \n<html lang='en'><head></head></html>", "an HTML page"},
		{"json object", `{"outbounds": [{"protocol": "vless"}]}`, "JSON"},
		{"json array", `[{"name": "synthetic", "server": "host.invalid"}]`, "JSON"},
		{"yaml document", "---\nproxies:\n  - name: synthetic\n    server: host.invalid\n", "YAML"},
		{"yaml mapping", "port: 7890\nmode: rule\nproxies: []\n", "YAML"},
		{"yaml list", "- name: synthetic\n  server: host.invalid\n", "YAML"},
		{"base64 list of links", b64(links), "a base64-encoded list of share links"},
		{"base64 list wrapped", b64(links)[:40] + "\n" + b64(links)[40:], "a base64-encoded list of share links"},
		{"base64 url alphabet", urlSafe, "a base64-encoded list of share links"},
		{"base64 url alphabet unpadded", urlSafeRaw, "a base64-encoded list of share links"},
		{"base64 unpadded", base64.RawStdEncoding.EncodeToString([]byte(links)), "a base64-encoded list of share links"},
		{"base64 other data", b64(strings.Repeat("synthetic payload ", 4)), "base64-encoded data"},
		{"link list", links, "a list of share links"},
		{"link vless", "vless://00000000-0000-0000-0000-000000000000@host.invalid:443?type=tcp#one", "a vless:// share link"},
		{"link vmess", "vmess://" + b64(`{"add":"host.invalid"}`), "a vmess:// share link"},
		{"link trojan", "trojan://synthetic@host.invalid:443", "a trojan:// share link"},
		{"link ss", "ss://" + b64("aes-128-gcm:synthetic") + "@host.invalid:8388", "a ss:// share link"},
		{"link hysteria2", "hysteria2://synthetic@host.invalid:443/?sni=host.invalid", "a hysteria2:// share link"},
		{"link hy2", "hy2://synthetic@host.invalid:443", "a hy2:// share link"},
		{"link tuic", "tuic://id:synthetic@host.invalid:443", "a tuic:// share link"},
		{"link uppercase scheme", "VLESS://synthetic@host.invalid:443", "a vless:// share link"},
		{"link with trailing newline", "trojan://synthetic@host.invalid:443\n\n", "a trojan:// share link"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseConfig([]byte(tt.body))
			if CodeOf(err) != FailureMalformedConfig {
				t.Fatalf("code = %s, err = %v", CodeOf(err), err)
			}
			want := "This is not a DobbyVPN subscription: the server returned " + tt.what +
				". DobbyVPN subscriptions use the TOML format, see " + formatURL
			var domain *Error
			if !errors.As(err, &domain) || domain.Message != want || domain.Cause != nil {
				t.Fatalf("error = %v, want message %q", err, want)
			}
		})
	}
}

func TestParseConfigEmptyAndNoProfilesTexts(t *testing.T) {
	for _, body := range []string{"", " \n\t\n"} {
		_, err := parseConfig([]byte(body))
		want := "The subscription is empty. Expected TOML with at least one [[Outline]], [[Xray]] or [[TrustTunnel]] profile, see " + formatURL
		if CodeOf(err) != FailureMalformedConfig || errMessage(err) != want {
			t.Fatalf("%q: error = %v", body, err)
		}
	}
	_, err := parseConfig([]byte("[ExcludeIPs]\nIPs = ['192.0.2.0/24']\n"))
	want := "The subscription contains no profiles. Add at least one [[Outline]], [[Xray]] or [[TrustTunnel]] section, see " + formatURL
	if CodeOf(err) != FailureMalformedConfig || errMessage(err) != want {
		t.Fatalf("no profiles error = %v", err)
	}
}

func TestParseConfigBrokenTOMLIsNotTakenForAForeignFormat(t *testing.T) {
	for name, raw := range map[string]string{
		"broken outline":          "[[Outline]]\nServer = 'vpn.invalid'\nPort = \n",
		"yaml-like inside":        "[[Xray]]\nserver: host.invalid\n",
		"colon instead of equals": "logs_endpoint: \"https://logs.invalid/\"\n\n[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n",
		"indented header":         "logs_endpoint: \"https://logs.invalid/\"\n  [[Xray]]  \n",
		"unterminated":            "[[Outline]\nServer = 'vpn.invalid'\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseConfig([]byte(raw))
			message := err.Error()
			if CodeOf(err) != FailureMalformedConfig || errors.Unwrap(err) == nil ||
				!strings.HasPrefix(message, "MALFORMED_CONFIG: This is not a valid DobbyVPN subscription: TOML could not be parsed: toml: ") ||
				!strings.HasSuffix(message, ". See "+formatURL) {
				t.Fatalf("error = %q", message)
			}
		})
	}
}

func TestParseConfigNamesTheBrokenProfileAndField(t *testing.T) {
	tests := []struct{ name, raw, want string }{
		{
			"second profile of three, described",
			diagnosticsOutlineProfile + "[[Outline]]\nDescription = 'My fast SS'\nServer = 'vpn.invalid'\nPort = 70000\nPassword = 'synthetic'\n\n" + diagnosticsOutlineProfile,
			`Subscription rejected. Profile 2 (Outline "My fast SS"), field Port: must be an integer from 1 to 65535.`,
		},
		{
			"numbering follows file order across protocols",
			"[[Xray]]\noutbounds = []\n[[TrustTunnel]]\n[[Outline]]\nPort = 443\nPassword = 'synthetic'\n",
			`Subscription rejected. Profile 3 (Outline), field Server: is required.`,
		},
		{
			"description is truncated and quoted",
			"[[Outline]]\nDescription = '" + strings.Repeat("x", 60) + "'\nServer = 'vpn.invalid'\nPort = 443\n",
			`Subscription rejected. Profile 1 (Outline "` + strings.Repeat("x", 40) + `..."), field Password: is required.`,
		},
		{
			"missing port",
			"[[Outline]]\nServer = 'vpn.invalid'\nPassword = 'synthetic'\n",
			`Subscription rejected. Profile 1 (Outline), field Port: is required unless WebSocket = true.`,
		},
		{
			"websocket type",
			"[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\nWebSocket = 'yes'\n",
			`Subscription rejected. Profile 1 (Outline), field WebSocket: must be true or false.`,
		},
		{
			"websocket path missing",
			"[[Outline]]\nServer = 'vpn.invalid'\nPassword = 'synthetic'\nWebSocket = true\n",
			`Subscription rejected. Profile 1 (Outline), field WebSocketPath: must be a non-empty string when WebSocket = true.`,
		},
		{
			"bad server",
			"[[Outline]]\nServer = 'https://vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n",
			`Subscription rejected. Profile 1 (Outline), field Server: must be a hostname or IP address.`,
		},
		{
			"password type",
			"[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 12\n",
			`Subscription rejected. Profile 1 (Outline), field Password: must be a string.`,
		},
		{
			"method type",
			"[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\nMethod = 5\n",
			`Subscription rejected. Profile 1 (Outline), field Method: must be a string.`,
		},
		{
			"description type",
			"[[Outline]]\nDescription = 7\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n",
			`Subscription rejected. Profile 1 (Outline), field Description: must be a string.`,
		},
		{
			"cloak type",
			"[[Outline]]\nCloak = 1\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n",
			`Subscription rejected. Profile 1 (Outline), field Cloak: must be true or false.`,
		},
		{
			"xray outbounds missing",
			diagnosticsOutlineProfile + "[[Xray]]\nDescription = 'x'\n",
			`Subscription rejected. Profile 2 (Xray "x"), field outbounds: is required.`,
		},
		{
			"xray outbounds string",
			"[[Xray]]\noutbounds = 'proxy'\n",
			`Subscription rejected. Profile 1 (Xray), field outbounds: must be an array.`,
		},
		{
			"xray outbounds table",
			"[[Xray]]\n[Xray.outbounds]\ntag = 'proxy'\n",
			`Subscription rejected. Profile 1 (Xray), field outbounds: must be an array.`,
		},
		{
			"trusttunnel endpoint string",
			"[[TrustTunnel]]\nendpoint = 'host.invalid'\n",
			`Subscription rejected. Profile 1 (TrustTunnel), field endpoint: must be a table.`,
		},
		{
			"trusttunnel endpoint array",
			"[[TrustTunnel]]\nendpoint = [1]\n",
			`Subscription rejected. Profile 1 (TrustTunnel), field endpoint: must be a table.`,
		},
		{
			"trusttunnel verification disabled",
			"[[TrustTunnel]]\n[TrustTunnel.endpoint]\nskip_verification = true\n",
			`Subscription rejected. Profile 1 (TrustTunnel), field endpoint.skip_verification: certificate verification must remain enabled.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := parseConfig([]byte(tt.raw))
			if CodeOf(err) != FailureMalformedConfig {
				t.Fatalf("code = %s, err = %v", CodeOf(err), err)
			}
			if got := errMessage(err); got != tt.want {
				t.Fatalf("message = %q, want %q", got, tt.want)
			}
			if parsed.profiles != nil {
				t.Fatalf("rejected subscription returned profiles")
			}
		})
	}
}

func TestParseConfigRemovedCloakStaysUnsupportedAndNamesTheProfile(t *testing.T) {
	_, err := parseConfig([]byte(diagnosticsOutlineProfile + "[[Outline]]\nCloak = true\n"))
	want := "Subscription rejected. Profile 2 (Outline), field Cloak: removed Cloak profiles are not supported."
	if CodeOf(err) != FailureUnsupported || errMessage(err) != want {
		t.Fatalf("error = %v", err)
	}
}

func TestParseConfigKeepsWorkingSubscriptionsAndWarns(t *testing.T) {
	read := captureLog(t)
	raw := "[[Outline]]\nDescription = 'ok'\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic-secret-value'\n" +
		"Method = ''\nDisguisePrefix = 5\nwebsocket = true\nMetod = 'x'\nTotallyCustom = 1\nWebSocketPath = '/synthetic-path'\n"
	parsed, err := parseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("subscription that connects today was rejected: %v", err)
	}
	if len(parsed.profiles) != 1 {
		t.Fatalf("profiles = %d", len(parsed.profiles))
	}
	// Empty Method keeps the default cipher, a non-string prefix is dropped.
	url := parsed.profiles[0].Config.OutlineURL
	if url != "ss://"+b64("chacha20-ietf-poly1305:synthetic-secret-value")+"@vpn.invalid:443" {
		t.Fatalf("normalized URL changed: %q", url)
	}
	logged := read()
	for _, want := range []string{
		`Profile 1: unknown field \"websocket\"`,
		`Profile 1: unknown field \"Metod\"`,
		`Profile 1: unknown field \"TotallyCustom\"`,
		`Profile 1: field \"DisguisePrefix\" must be a string, ignoring it`,
		`Profile 1: field \"WebSocketPath\" is ignored because WebSocket is not true`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q\n%s", want, logged)
		}
	}
	if strings.Contains(logged, "did you mean") {
		t.Errorf("warning carries a suggestion:\n%s", logged)
	}
	for _, secret := range []string{"synthetic-secret-value", "vpn.invalid", "synthetic-path"} {
		if strings.Contains(logged, secret) {
			t.Errorf("warning leaks %q:\n%s", secret, logged)
		}
	}
}

func TestParseConfigWarningsUseSubscriptionNumbering(t *testing.T) {
	read := captureLog(t)
	raw := "[[Xray]]\noutbounds = []\n" + diagnosticsOutlineProfile + "[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\nprefix = 'POST '\n"
	if _, err := parseConfig([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if logged := read(); !strings.Contains(logged, `Profile 3: unknown field \"prefix\"`) {
		t.Fatalf("log = %s", logged)
	}
}

func TestParseConfigDoesNotWarnWhenSubscriptionIsRejected(t *testing.T) {
	read := captureLog(t)
	raw := "[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\nwebsocket = true\n\n[[Outline]]\nServer = 'vpn.invalid'\nPort = 0\nPassword = 'synthetic'\n"
	if _, err := parseConfig([]byte(raw)); err == nil {
		t.Fatal("broken subscription accepted")
	}
	if logged := read(); strings.Contains(logged, "unknown field") {
		t.Fatalf("warning logged for rejected subscription: %s", logged)
	}
}

func TestParseConfigWhitespaceMethodKeepsDefault(t *testing.T) {
	raw := "[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\nMethod = '   '\n"
	parsed, err := parseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("whitespace Method rejected: %v", err)
	}
	if !strings.HasPrefix(parsed.profiles[0].Config.OutlineURL, "ss://"+b64("chacha20-ietf-poly1305:synthetic")) {
		t.Fatalf("whitespace Method did not keep the default: %q", parsed.profiles[0].Config.OutlineURL)
	}
}

func errMessage(err error) string {
	var domain *Error
	if errors.As(err, &domain) {
		return domain.Message
	}
	return ""
}

// captureLog points the process logger at a temporary file and returns a
// reader of everything written so far.
func captureLog(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "service.log")
	if err := log.SetPath(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return func() string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
}

func TestParseConfigBoundsUnknownFieldWarnings(t *testing.T) {
	read := captureLog(t)
	var raw strings.Builder
	raw.WriteString("[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 'synthetic'\n")
	for i := 0; i < 60; i++ {
		raw.WriteString("k" + strconv.Itoa(i) + "=0\n")
	}
	if _, err := parseConfig([]byte(raw.String())); err != nil {
		t.Fatal(err)
	}
	logged := read()
	if got := strings.Count(logged, "unknown field"); got != 21 {
		t.Fatalf("logged %d unknown-field lines, want 20 warnings and one summary", got)
	}
	if !strings.Contains(logged, "40 more unknown fields not listed") {
		t.Fatalf("summary line missing: %.300s", logged)
	}
}

func TestParseConfigWarnsAboutTrustTunnelWithoutEndpoint(t *testing.T) {
	read := captureLog(t)
	raw := diagnosticsOutlineProfile + diagnosticsOutlineProfile + "[[TrustTunnel]]\nDescription = 'synthetic-name'\nvpn_mode = 'general'\n"
	parsed, err := parseConfig([]byte(raw))
	if err != nil {
		t.Fatalf("TrustTunnel profile without endpoint was rejected: %v", err)
	}
	if len(parsed.profiles) != 3 {
		t.Fatalf("profiles = %d", len(parsed.profiles))
	}
	logged := read()
	if !strings.Contains(logged, `Profile 3: field \"endpoint\" is missing; the profile will fail to connect`) {
		t.Fatalf("warning missing: %s", logged)
	}
	if strings.Contains(logged, "synthetic-name") {
		t.Fatalf("warning leaks the description: %s", logged)
	}
}

func TestParseConfigDoesNotWarnWhenTrustTunnelHasEndpoint(t *testing.T) {
	read := captureLog(t)
	raw := "[[TrustTunnel]]\n[TrustTunnel.endpoint]\nhostname = 'vpn.invalid'\n"
	if _, err := parseConfig([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	if logged := read(); strings.Contains(logged, "is missing") {
		t.Fatalf("unexpected warning: %s", logged)
	}
}
