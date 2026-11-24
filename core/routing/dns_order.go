package routing

import (
	"fmt"
	"strings"
)

// SummarizeScutilDNS reduces the unscoped part of `scutil --dns` output to its
// resolvers in the order the system lists them, one line each, such as
// "#1 domain=. servers=192.168.1.1 order=200000 if=4 (en0)". The log shows
// whether the tunnel resolver comes before the physical ones (#532).
func SummarizeScutilDNS(output string) []string {
	var summary []string
	var current []string
	var servers []string
	flush := func() {
		if current == nil {
			return
		}
		if len(servers) > 0 {
			current = append(current, "servers="+strings.Join(servers, ","))
		}
		summary = append(summary, strings.Join(current, " "))
		current, servers = nil, nil
	}
	for _, line := range strings.Split(output, "\n") {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "DNS configuration (for scoped queries)") {
			break
		}
		if number, ok := strings.CutPrefix(text, "resolver #"); ok {
			flush()
			current = []string{"#" + number}
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch {
		case strings.HasPrefix(key, "nameserver["):
			servers = append(servers, value)
		case key == "domain", key == "order", key == "flags":
			current = append(current, fmt.Sprintf("%s=%s", key, value))
		case key == "if_index":
			current = append(current, "if="+value)
		}
	}
	flush()
	return summary
}
