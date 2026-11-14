package sessionapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"core/log"
)

// subscriptionFormatURL points at the description of the subscription format.
const subscriptionFormatURL = "https://github.com/DobbyVPN/DobbyVPN#using-dobbyvpn"

const (
	fieldDescription              = "Description"
	fieldServer                   = "Server"
	fieldPort                     = "Port"
	fieldPassword                 = "Password"
	fieldMethod                   = "Method"
	fieldDisguisePrefix           = "DisguisePrefix"
	fieldWebSocket                = "WebSocket"
	fieldWebSocketPath            = "WebSocketPath"
	fieldCloak                    = "Cloak"
	fieldOutbounds                = "outbounds"
	fieldEndpoint                 = "endpoint"
	fieldSkipVerification         = "skip_verification"
	fieldEndpointSkipVerification = "endpoint.skip_verification"

	reasonRequired = "is required"
	reasonString   = "must be a string"
	reasonBoolean  = "must be true or false"

	defaultOutlineMethod = "chacha20-ietf-poly1305"
	logCategoryConfig    = "CONFIGURATION"

	maxUnknownFieldWarnings = 20
	maxDescriptionInError   = 40
	minBase64Payload        = 24
)

var shareLinkSchemes = []string{
	"vless", "vmess", "trojan", "ss", "ssr", "hysteria", "hysteria2", "hy2", "tuic",
	"wireguard", "socks", "socks4", "socks5", "anytls", "juicity",
}

var (
	profileHeaderRE = regexp.MustCompile(`(?m)^\s*\[\[\s*(Outline|Xray|TrustTunnel)\s*\]\]`)
	yamlKeyLineRE   = regexp.MustCompile(`^["']?[A-Za-z0-9_.-][^=:]*["']?:(\s|$)`)
	base64BodyRE    = regexp.MustCompile(`^[A-Za-z0-9+/_-]+={0,2}$`)
)

// profileRef identifies a profile the way the client lists show it: the
// one-based position across all sections in file order.
type profileRef struct {
	number      int
	protocol    Protocol
	description string
}

func (p profileRef) label() string {
	name := protocolDisplayName(p.protocol)
	if p.description != "" {
		return fmt.Sprintf("Profile %d (%s %q)", p.number, name, truncateRunes(p.description, maxDescriptionInError))
	}
	return fmt.Sprintf("Profile %d (%s)", p.number, name)
}

func protocolDisplayName(protocol Protocol) string {
	switch protocol {
	case ProtocolOutline:
		return configSectionOutline
	case ProtocolXray:
		return configSectionXray
	case ProtocolTrustTunnel:
		return configSectionTrustTunnel
	default:
		return string(protocol)
	}
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

// fieldError is a rejected value inside one profile. parseProfile attaches the
// profile number to it.
type fieldError struct {
	code   FailureCode
	field  string
	reason string
}

func (e *fieldError) Error() string { return "field " + e.field + ": " + e.reason }

func invalidField(field, reason string) error {
	return &fieldError{code: FailureMalformedConfig, field: field, reason: reason}
}

func (p profileRef) reject(err error) error {
	var fe *fieldError
	if errors.As(err, &fe) {
		return failure(fe.code, fmt.Sprintf("Subscription rejected. %s, field %s: %s.", p.label(), fe.field, fe.reason))
	}
	return err
}

// profileWarning is an accepted-but-suspicious setting. It carries only the
// profile number and a field name, never a value.
type profileWarning struct {
	field   string
	message string
}

func (p profileRef) emit(warnings []profileWarning) {
	for _, warning := range warnings {
		log.Warn(logCategoryConfig, fmt.Sprintf("Profile %d: %s", p.number, warning.message),
			map[string]any{"profile": p.number, "field": warning.field})
	}
}

var knownOutlineFields = []string{
	fieldDescription, fieldServer, fieldPort, fieldPassword, fieldMethod, fieldDisguisePrefix, fieldWebSocket, fieldWebSocketPath, fieldCloak,
}

// profileSettingWarnings lists settings that are accepted but have no effect.
// They stay warnings so that subscriptions that connect today keep connecting.
func profileSettingWarnings(protocol Protocol, block map[string]interface{}) []profileWarning {
	if protocol != ProtocolOutline {
		return nil
	}
	warnings := unknownOutlineFieldWarnings(block)
	if value, present := block[fieldDisguisePrefix]; present {
		if _, isString := value.(string); !isString {
			warnings = append(warnings, profileWarning{field: fieldDisguisePrefix, message: `field "DisguisePrefix" must be a string, ignoring it`})
		}
	}
	if websocket, _ := block[fieldWebSocket].(bool); !websocket {
		if _, present := block[fieldWebSocketPath]; present {
			warnings = append(warnings, profileWarning{field: fieldWebSocketPath, message: `field "WebSocketPath" is ignored because WebSocket is not true`})
		}
	}
	return warnings
}

func unknownOutlineFieldWarnings(block map[string]interface{}) []profileWarning {
	var warnings []profileWarning
	unlisted := 0
	for _, key := range sortedKeys(block) {
		if isKnownOutlineField(key) {
			continue
		}
		if len(warnings) >= maxUnknownFieldWarnings {
			unlisted++
			continue
		}
		name := truncateRunes(key, maxDescriptionInError)
		message := fmt.Sprintf("unknown field %q", name)
		if suggestion := suggestField(key, knownOutlineFields); suggestion != "" {
			message += fmt.Sprintf(", did you mean %q?", suggestion)
		}
		warnings = append(warnings, profileWarning{field: name, message: message})
	}
	if unlisted > 0 {
		warnings = append(warnings, profileWarning{field: "unknown", message: fmt.Sprintf("%d more unknown fields not listed", unlisted)})
	}
	return warnings
}

func isKnownOutlineField(key string) bool {
	for _, known := range knownOutlineFields {
		if key == known {
			return true
		}
	}
	return false
}

func sortedKeys(block map[string]interface{}) []string {
	keys := make([]string, 0, len(block))
	for key := range block {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func suggestField(key string, candidates []string) string {
	for _, candidate := range candidates {
		if strings.EqualFold(key, candidate) {
			return candidate
		}
	}
	best, bestDistance := "", 3
	for _, candidate := range candidates {
		if d := editDistance(strings.ToLower(key), strings.ToLower(candidate)); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	if len([]rune(key)) < 4 {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(br)]
}

// withFormatPointer keeps the parser error in the chain and appends the pointer
// to the format description after it in the displayed text.
type withFormatPointer struct{ err error }

func (e withFormatPointer) Error() string { return e.err.Error() + ". See " + subscriptionFormatURL }
func (e withFormatPointer) Unwrap() error { return e.err }

// undecodableConfigError explains a body that is not valid TOML: either a
// recognised foreign format or a broken DobbyVPN subscription.
func undecodableConfigError(raw []byte, decodeErr error) error {
	if what, foreign := detectForeignFormat(raw); foreign {
		return notDobbySubscription(what)
	}
	return failureWithCause(FailureMalformedConfig, "This is not a valid DobbyVPN subscription: TOML could not be parsed", withFormatPointer{decodeErr})
}

func notDobbySubscription(what string) error {
	return failure(FailureMalformedConfig,
		"This is not a DobbyVPN subscription: the server returned "+what+". DobbyVPN subscriptions use the TOML format, see "+subscriptionFormatURL)
}

// detectForeignFormat names a well-known non-DobbyVPN body. It is consulted
// only after TOML decoding failed, and never for text that contains a
// DobbyVPN profile header (that is a broken subscription, not a foreign one).
func detectForeignFormat(raw []byte) (string, bool) {
	text := strings.TrimSpace(strings.TrimPrefix(string(raw), "\ufeff"))
	if text == "" || profileHeaderRE.MatchString(text) {
		return "", false
	}
	if strings.HasPrefix(text, "<") {
		return "an HTML page", true
	}
	if (text[0] == '{' || text[0] == '[') && json.Valid([]byte(text)) {
		return "JSON", true
	}
	if what, ok := describeShareLinks(text); ok {
		return what, true
	}
	if what, ok := describeBase64(text); ok {
		return what, true
	}
	if looksLikeYAML(text) {
		return "YAML", true
	}
	return "", false
}

func describeShareLinks(text string) (string, bool) {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "", false
	}
	first := ""
	for _, line := range lines {
		scheme, ok := shareLinkScheme(line)
		if !ok {
			return "", false
		}
		if first == "" {
			first = scheme
		}
	}
	if len(lines) == 1 {
		return "a " + first + ":// share link", true
	}
	return "a list of share links", true
}

func shareLinkScheme(line string) (string, bool) {
	index := strings.Index(line, "://")
	if index <= 0 || index+3 >= len(line) || strings.ContainsAny(line, " \t") {
		return "", false
	}
	scheme := strings.ToLower(line[:index])
	for _, known := range shareLinkSchemes {
		if scheme == known {
			return scheme, true
		}
	}
	return "", false
}

func describeBase64(text string) (string, bool) {
	compact := strings.Join(strings.Fields(text), "")
	if len(compact) < 16 || !base64BodyRE.MatchString(compact) {
		return "", false
	}
	decoded, ok := decodeBase64Any(compact)
	if !ok {
		return "", false
	}
	if _, isLinks := describeShareLinks(strings.TrimSpace(string(decoded))); isLinks {
		return "a base64-encoded list of share links", true
	}
	if len(compact) >= minBase64Payload {
		return "base64-encoded data", true
	}
	return "", false
}

func decodeBase64Any(text string) ([]byte, bool) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(text); err == nil {
			return decoded, true
		}
	}
	return nil, false
}

func looksLikeYAML(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line == "---" || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "- ") || yamlKeyLineRE.MatchString(line)
	}
	return false
}
