// Package publicip looks up the public IP observed from the caller's network
// vantage point through plain HTTPS echo services.
package publicip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultEndpoints are queried in order until one returns a well-formed address.
var DefaultEndpoints = []string{"https://api.ipify.org", "https://ifconfig.me/ip"}

// maxResponseBytes bounds a response body; IPv4 and IPv6 text plus the usual
// trailing newline fit within 64 bytes.
const maxResponseBytes = 64

// Lookup GETs each endpoint in order and returns the first response body that
// trims to a single IPv4 or IPv6 address. Every failure is joined so callers
// can report all of the endpoints they tried.
func Lookup(ctx context.Context, client *http.Client, endpoints []string) (string, error) {
	var failures []error
	for _, endpoint := range endpoints {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
		if err != nil {
			failures = append(failures, fmt.Errorf("create external IP request: %w", err))
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			failures = append(failures, fmt.Errorf("external IP request failed: %w", err))
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		closeErr := response.Body.Close()
		if closeErr != nil {
			failures = append(failures, fmt.Errorf("close external IP response: %w", closeErr))
			continue
		}
		if readErr != nil {
			failures = append(failures, fmt.Errorf("read external IP response: %w", readErr))
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			failures = append(failures, fmt.Errorf("external IP response status=%d body=%q", response.StatusCode, body))
			continue
		}
		address, parseErr := netip.ParseAddr(strings.TrimSpace(string(body)))
		if len(body) <= maxResponseBytes && parseErr == nil && address.Zone() == "" {
			return address.String(), nil
		}
		failures = append(failures, errors.New("external IP response is not a single IP address"))
	}
	if len(failures) == 0 {
		return "", errors.New("external IP lookup has no endpoints")
	}
	return "", errors.Join(failures...)
}
