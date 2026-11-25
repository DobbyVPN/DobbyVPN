package log

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
)

// redacted is the fixed marker replacing URLs, hostnames and IP addresses in
// persisted log text. Ports and protocols are kept.
const redacted = "[redacted]"

// Patterns are applied in order: URLs first (they embed hosts), then bracketed
// IPv6, dotted IPv4, colon-separated runs (IPv6 and MAC), then domain names.
// Known accepted over-match: dotted code identifiers in free text (package
// names, file names like MainActivity.kt or config.json) also match domainName
// and render as [redacted]; distinguishing them from domains is not reliable
// and the privacy-safe direction is chosen deliberately.
var (
	urlPattern  = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.\-]*://[^\s"\\]+`)
	bracketIPv6 = regexp.MustCompile(`\[[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*\]`)
	dottedIPv4  = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	colonRun    = regexp.MustCompile(`\b[0-9A-Fa-f]*(?::[0-9A-Fa-f]*){3,}\b`)
	domainName  = regexp.MustCompile(`\b(?:[A-Za-z0-9-]+\.)+[A-Za-z]{2,}\b`)
)

func redactText(text string) string {
	text = urlPattern.ReplaceAllString(text, redacted)
	text = bracketIPv6.ReplaceAllString(text, redacted)
	text = dottedIPv4.ReplaceAllString(text, redacted)
	text = colonRun.ReplaceAllString(text, redacted)
	text = domainName.ReplaceAllString(text, redacted)
	return text
}

// redactValue scrubs strings and composite values through their JSON form (the
// same serialization the slog handler performs), preserving the value's shape.
func redactValue(value any) any {
	switch typed := value.(type) {
	case nil, bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64:
		return value
	case string:
		return redactText(typed)
	case []byte:
		return redactText(string(typed))
	case *url.URL:
		if typed == nil {
			return typed
		}
		// A URL value's struct JSON form would keep its path and query in clear;
		// its string form is the meaningful one and redacts as a whole URL.
		return redactText(typed.String())
	case url.URL:
		return redactText(typed.String())
	case json.RawMessage:
		return json.RawMessage(redactText(string(typed)))
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return redactText(fmt.Sprintf("%v", typed))
		}
		return json.RawMessage(redactText(string(data)))
	}
}

func redactArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	scrubbed := make(map[string]any, len(arguments))
	for key, value := range arguments {
		scrubbed[key] = redactValue(value)
	}
	return scrubbed
}
