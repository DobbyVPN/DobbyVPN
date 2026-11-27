package routing

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MacOSPFAnchor is the specific anchor used by pre-Network Extension installations.
const MacOSPFAnchor = "com.apple/dobbyvpn"

//nolint:unused // Used on macOS and exercised by portable Torturer tests.
func pfHasReference(output, token string) (bool, error) {
	table := false
	known := false
	column := -1
	for _, line := range strings.Split(output, "\n") {
		text := strings.TrimSpace(line)
		if text == "No pf starter references held" || text == "No pf_enabled references" {
			return false, nil
		}
		if text == "TOKENS:" {
			table = true
			known = true
			continue
		}
		if !table || text == "" {
			continue
		}
		if strings.Contains(line, "Process Name") && strings.Contains(line, "TOKEN") {
			column = strings.Index(line, "TOKEN")
			continue
		}
		if column < 0 || len(line) <= column {
			return false, fmt.Errorf("invalid pf reference row: %s", line)
		}
		fields := strings.Fields(line[column:])
		value := ""
		if len(fields) > 0 {
			value = fields[0]
		}
		if _, err := strconv.ParseUint(value, 10, 64); err != nil {
			return false, fmt.Errorf("invalid pf reference token row: %s", line)
		}
		if value == token {
			return true, nil
		}
	}
	if !known {
		return false, errors.New("pfctl printed no recognizable reference inventory")
	}
	return false, nil
}
