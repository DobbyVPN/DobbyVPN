package sessionapi

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"core/log"
)

// FuzzParseConfig checks the session configuration TOML parser (parseConfig,
// which calls decodeConfig) against untrusted bytes: it may return an error,
// but it must never panic, accept an oversized input, or report success
// without a coherent parsed configuration. The seeds mirror the valid and
// malformed unit cases in config_diagnostics_test.go plus boundary sizes,
// truncated input and unexpected field/type combinations.
func FuzzParseConfig(f *testing.F) {
	// parseConfig emits bounded warnings for accepted subscriptions through
	// core/log, which otherwise buffers events in memory for the whole
	// process. Scope a log file to this test so a fuzz run stays bounded and
	// the captureLog-based tests in config_diagnostics_test.go keep their own.
	if err := log.SetPath(filepath.Join(f.TempDir(), "fuzz-warnings.log")); err != nil {
		f.Fatalf("open fuzz warning log: %v", err)
	}
	f.Cleanup(func() { _ = log.Close() })

	links := "trojan://synthetic@host.invalid:443"
	paddedPrefix := diagnosticsOutlineProfile + "\n# "
	if len(paddedPrefix)+1 > maxConfigBytes {
		f.Fatalf("configuration fixtures exceed the size limit")
	}
	atLimit := paddedPrefix + strings.Repeat("x", maxConfigBytes-len(paddedPrefix)-1) + "\n"

	for _, seed := range [][]byte{
		[]byte(diagnosticsOutlineProfile),
		[]byte("[[Xray]]\noutbounds = []\n"),
		[]byte("[[TrustTunnel]]\n[TrustTunnel.endpoint]\nhostname = 'vpn.invalid'\n"),
		[]byte("[[Xray]]\noutbounds = []\n[[TrustTunnel]]\n[[Outline]]\nPort = 443\nPassword = 'synthetic'\n"),
		[]byte(""),
		[]byte(" \n\t\n"),
		[]byte("[ExcludeIPs]\nIPs = ['192.0.2.0/24']\n"),
		[]byte("[[Outline]\nServer = 'vpn.invalid'\n"),
		[]byte("<html><body><pre>\n[[Outline]]\n</pre></body></html>"),
		[]byte(links + "\n"),
		[]byte(b64(links)),
		[]byte(diagnosticsOutlineProfile[:len(diagnosticsOutlineProfile)/2]),
		[]byte("[[Outline]]\nServer = 'vpn.invalid'\nPort = '443'\nPassword = 'synthetic'\n"),
		[]byte("[[Outline]]\nServer = 'vpn.invalid'\nPort = 443\nPassword = 12\n"),
		[]byte("[[Outline]]\nCloak = 1\n"),
		[]byte("[[Xray]]\noutbounds = 'proxy'\n"),
		[]byte("[[TrustTunnel]]\nendpoint = 'host.invalid'\n"),
		[]byte("[[TrustTunnel]]\n[TrustTunnel.endpoint]\nskip_verification = true\n"),
		[]byte(atLimit),
		[]byte(atLimit + "x"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		parsed, err := parseConfig(raw)
		if len(raw) > maxConfigBytes {
			if err == nil {
				t.Fatalf("oversized configuration (%d bytes) was accepted", len(raw))
			}
			if code := CodeOf(err); code != FailureMalformedConfig {
				t.Fatalf("oversized configuration code = %s, err = %v", code, err)
			}
			if message := errMessage(err); message != "configuration exceeds the 1 MiB size limit" {
				t.Fatalf("oversized configuration message = %q", message)
			}
			if parsed.digest != "" || parsed.profiles != nil {
				t.Fatalf("rejected oversized configuration returned a parsed value")
			}
			return
		}
		if err != nil {
			if parsed.digest != "" || parsed.profiles != nil {
				t.Fatalf("rejected configuration returned a parsed value")
			}
			return
		}
		digest := sha256.Sum256(raw)
		if parsed.digest != hex.EncodeToString(digest[:]) {
			t.Fatalf("digest = %q, want the sha256 of the input", parsed.digest)
		}
		if len(parsed.profiles) == 0 {
			t.Fatalf("accepted configuration returned no profiles")
		}
		for i, profile := range parsed.profiles {
			if profile.Summary.Index != int32(i) {
				t.Fatalf("profile %d has index %d", i, profile.Summary.Index)
			}
			switch profile.Summary.Protocol {
			case ProtocolOutline, ProtocolXray, ProtocolTrustTunnel:
			default:
				t.Fatalf("profile %d has unknown protocol %q", i, profile.Summary.Protocol)
			}
		}
	})
}
