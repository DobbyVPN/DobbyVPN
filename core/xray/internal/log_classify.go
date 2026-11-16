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

// routineEndings are the wordings of an ordinary connection end: a socket being closed during teardown.
var routineEndings = []string{
	"use of closed network connection",
	"io: read/write on closed pipe",
	"io: read on closed pipe",
	"io: write on closed pipe",
	"context canceled",
}

// classifyXrayMessage returns the severity Xray's message should be logged at.
// Per-connection chatter and routine connection ends drop to debug, and the
// start notice that Xray words as a warning is shown as info.
func classifyXrayMessage(severity xrayLog.Severity, content string) xrayLog.Severity {
	body := strings.ToLower(content)
	// An unexplained peer reset can indicate a broken tunnel, even when Xray
	// emits the connection failure at info level. Keep it visible by default.
	if severity == xrayLog.Severity_Info &&
		(strings.Contains(body, "failed to process outbound traffic") || strings.Contains(body, "connection ends")) &&
		hasConnectionFault(body) {
		return xrayLog.Severity_Warning
	}
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
	// Xray words ended connections as "failed to process outbound traffic > ...
	// connection ends > ..." at info, and some other endings at warning or error.
	endsConnection := strings.Contains(body, "connection ends")
	if severity == xrayLog.Severity_Info && endsConnection && IsRoutineConnectionEnd(content) {
		return xrayLog.Severity_Debug
	}
	return severity
}

// modulePrefix matches the "app/proxyman/outbound: " style module labels.
var modulePrefix = regexp.MustCompile(`^(?:[a-z][a-z0-9_]*(?:/[a-z0-9_]+)*:\s+)+`)

func hasConnectionFault(text string) bool {
	lower := strings.ToLower(text)
	for _, fault := range []string{"connection reset by peer", "forcibly closed by the remote host", "rst_stream", "too_many_pings", "enhance_your_calm"} {
		if strings.Contains(lower, fault) {
			return true
		}
	}
	return false
}

// IsRoutineConnectionEnd reports whether text describes the ordinary end of a
// connection rather than a fault in the tunnel.
func IsRoutineConnectionEnd(text string) bool {
	if hasConnectionFault(text) {
		return false
	}
	lower := strings.ToLower(text)
	for _, ending := range routineEndings {
		if strings.Contains(lower, ending) {
			return true
		}
	}
	return false
}
