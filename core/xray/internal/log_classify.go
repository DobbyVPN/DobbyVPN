package internal

import (
	"regexp"
	"strings"

	xrayLog "github.com/xtls/xray-core/common/log"
)

// perConnectionPrefixes mark Xray lines emitted once per proxied connection.
// They are useful when debugging but dominate the log at info level.
var perConnectionPrefixes = []string{
	"sniffed domain",
	"tcp connect request",
	"default route",
	"tunneling request",
	"accepted",
}

// isStreamEndMessage reports a per-connection ending, the messages whose
// reasons must survive the bridge threshold.
func isStreamEndMessage(content string) bool {
	body := strings.ToLower(content)
	return strings.Contains(body, "connection ends") || strings.Contains(body, "failed to process outbound traffic")
}

// classifyXrayMessage returns the severity Xray's message should be logged at.
// A connection ending surfaces at warning when its reason points at a stream
// failure, and at debug otherwise; per-connection chatter drops to debug, and
// the start notice that Xray words as a warning is shown as info.
func classifyXrayMessage(severity xrayLog.Severity, content string) xrayLog.Severity {
	if isStreamEndMessage(content) {
		switch severity {
		case xrayLog.Severity_Warning, xrayLog.Severity_Error:
			return severity
		case xrayLog.Severity_Info:
			// A bare ending (EOF, a closed socket, routine wording) is normal
			// teardown; only an ending that names a stream failure stays
			// visible at the default threshold.
			if hasStreamFailureReason(content) {
				return xrayLog.Severity_Warning
			}
			return xrayLog.Severity_Debug
		default:
			return severity
		}
	}
	body := strings.ToLower(content)
	// Real lines look like "[123456] app/dispatcher: sniffed domain: host" or
	// "core: Xray 26.6.27 started": an optional bracketed context, then one or
	// more "module/path: " prefixes, then the text.
	if strings.HasPrefix(body, "[") {
		if end := strings.Index(body, "] "); end >= 0 {
			body = body[end+2:]
		}
	}
	body = modulePrefix.ReplaceAllString(body, "")
	if severity == xrayLog.Severity_Warning && strings.HasPrefix(body, "xray ") && strings.HasSuffix(strings.TrimSpace(body), " started") {
		return xrayLog.Severity_Info
	}
	if severity == xrayLog.Severity_Info {
		for _, prefix := range perConnectionPrefixes {
			if strings.HasPrefix(body, prefix) {
				return xrayLog.Severity_Debug
			}
		}
	}
	return severity
}

// modulePrefix matches the "app/proxyman/outbound: " style module labels.
var modulePrefix = regexp.MustCompile(`^(?:[a-z][a-z0-9_]*(?:/[a-z0-9_]+)*:\s+)+`)

// hasStreamFailureReason reports whether text names a stream failure rather
// than an ordinary teardown.
func hasStreamFailureReason(text string) bool {
	lower := strings.ToLower(text)
	for _, failure := range []string{
		"connection reset by peer",
		"forcibly closed by the remote host",
		"rst_stream",
		"too_many_pings",
		"enhance_your_calm",
		"broken pipe",
		"aborted by the software",
		"i/o timeout",
		"deadline exceeded",
		"connection refused",
		"no route to host",
		"network is unreachable",
		"handshake failure",
		"handshake error",
	} {
		if strings.Contains(lower, failure) {
			return true
		}
	}
	return false
}
