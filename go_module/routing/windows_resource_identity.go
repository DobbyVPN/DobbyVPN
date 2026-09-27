package routing

import "strings"

const (
	ownedWindowsTunnelPrefix   = "DobbyVPN-"
	ownedWindowsIPv6RulePrefix = "DobbyVPN Block IPv6 "
)

// IsOwnedWindowsTunnelInterface reports whether name is one of the random
// aliases generated for a DobbyVPN Wintun adapter.
func IsOwnedWindowsTunnelInterface(name string) bool {
	if !strings.HasPrefix(name, ownedWindowsTunnelPrefix) {
		return false
	}
	identity := strings.TrimPrefix(name, ownedWindowsTunnelPrefix)
	if len(identity) != 32 {
		return false
	}
	for _, char := range identity {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// IsOwnedWindowsTunRedirect reports whether the route tuple is one of the
// exact on-link split-default routes installed on a DobbyVPN Wintun adapter.
func IsOwnedWindowsTunRedirect(prefix, nextHop, interfaceName string) bool {
	if !IsOwnedWindowsTunnelInterface(interfaceName) || nextHop != "0.0.0.0" {
		return false
	}
	return prefix == "0.0.0.0/1" || prefix == "128.0.0.0/1"
}

// IsOwnedWindowsIPv6RuleName reports whether name has the exact session-name
// form emitted by the Windows runtime for its IPv6 block rule.
func IsOwnedWindowsIPv6RuleName(name string) bool {
	if !strings.HasPrefix(name, ownedWindowsIPv6RulePrefix) {
		return false
	}
	session := strings.TrimPrefix(name, ownedWindowsIPv6RulePrefix)
	if !strings.HasPrefix(session, "windows-") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(session, "windows-"), "-")
	if len(parts) != 2 {
		return false
	}
	return decimalDigits(parts[0]) && decimalDigits(parts[1])
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
