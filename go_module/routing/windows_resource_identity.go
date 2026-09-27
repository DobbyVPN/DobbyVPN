package routing

import "strings"

const (
	ownedWindowsIPv6RulePrefix = "DobbyVPN Block IPv6 "
)

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
