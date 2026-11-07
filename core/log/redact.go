package log

import (
	"encoding/json"
	"regexp"
	"strings"
)

const redacted = "REDACTED"

var (
	// Proxy/share links carry their credentials in the userinfo or the body.
	shareLinkPattern = regexp.MustCompile(`(?i)\b((?:ss|ssr|vless|vmess|trojan|hysteria2?|hy2|tuic|socks5h?|socks4a?|wireguard)://)[^\s"'<>]+`)
	// Subscription/configuration URLs carry their token in the path or query.
	httpURLPattern = regexp.MustCompile(`(?i)\b(https?://[^\s/"'<>?#]+)[/?#][^\s"'<>]*`)
	// scheme://user:password@host and bare user:password@host (SOCKS endpoints).
	userinfoPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)?[^\s:/@"'<>=]+:[^\s@/"'<>]+@`)
	queryPattern    = regexp.MustCompile(`(?i)([?&;\s](?:access_token|token|auth|api_?key|key|password|passwd|pwd|secret|sig|signature|sub|subscription)=)[^&\s"'#]+`)
	sensitiveKey    = regexp.MustCompile(`(?i)password|passwd|secret|token|api_?key|authorization|credential|subscription|source_url|config_url`)
)

// redactString removes credentials and subscription tokens from free text.
func redactString(text string) string {
	if text == "" {
		return text
	}
	text = shareLinkPattern.ReplaceAllString(text, "${1}"+redacted)
	text = httpURLPattern.ReplaceAllString(text, "${1}/"+redacted)
	text = userinfoPattern.ReplaceAllString(text, "${1}"+redacted+"@")
	return queryPattern.ReplaceAllString(text, "${1}"+redacted)
}

// redactValue returns a redacted copy of a log argument without mutating the
// caller's data. Unknown types are passed through unchanged.
func redactValue(value any) any {
	switch typed := value.(type) {
	case string:
		return redactString(typed)
	case error:
		if typed == nil {
			return value
		}
		return redactString(typed.Error())
	case json.RawMessage:
		return json.RawMessage(redactString(string(typed)))
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = redactString(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactValue(item)
		}
		return out
	case map[string]any:
		return redactArguments(typed)
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, item := range typed {
			if sensitiveKey.MatchString(key) {
				out[key] = redacted
			} else {
				out[key] = redactString(item)
			}
		}
		return out
	}
	return value
}

func redactArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	out := make(map[string]any, len(arguments))
	for key, value := range arguments {
		if sensitiveKey.MatchString(key) && !isEmpty(value) {
			out[key] = redacted
			continue
		}
		out[key] = redactValue(value)
	}
	return out
}

func isEmpty(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}
