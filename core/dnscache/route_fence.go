//go:build windows

package dnscache

import (
	"errors"
	"fmt"
	"sync"
)

// routeFence retains each actual DNS socket route until the answer is accepted.
type routeFence struct {
	mu     sync.Mutex
	routes []dnsRouteObservation
}
type dnsRouteObservation struct {
	destination string
	identity    string
	preferred   uint64
}

func (f *routeFence) record(destination, identity string, preferred uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = append(f.routes, dnsRouteObservation{destination, identity, preferred})
}
func (f *routeFence) verify(selectRoute func(string, uint64) (string, error)) error {
	f.mu.Lock()
	routes := append([]dnsRouteObservation(nil), f.routes...)
	f.mu.Unlock()
	var failures []error
	for _, route := range routes {
		current, err := selectRoute(route.destination, route.preferred)
		if err != nil {
			failures = append(failures, fmt.Errorf("DNS route for %s: %w", route.destination, err))
			continue
		}
		if current != route.identity {
			failures = append(failures, fmt.Errorf("DNS route changed for %s", route.destination))
		}
	}
	return errors.Join(failures...)
}
