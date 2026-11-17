//go:build !(android || ios)

package executor

import (
	"context"
	"encoding/json"
	"time"

	"core/log"
	"core/sessionapi/mobilebinding"
)

const (
	// The same window the macOS app uses: one network change produces a burst
	// of OS notifications that should reach the core as a single event.
	networkEventDebounce = time.Second
	eventWake            = "wake"
	eventNetworkChanged  = "network_path_changed"
)

// newNetworkEvents returns a non-blocking emit function. Events within one
// debounce window reach the core as a single call; "wake" wins over a network
// change reported in the same burst.
func newNetworkEvents() func(reason string) {
	events := make(chan string, 16)
	go forwardNetworkEvents(events, networkEventDebounce, sendNetworkEvent)
	return func(reason string) {
		select {
		case events <- reason:
		default:
		}
	}
}

func forwardNetworkEvents(events <-chan string, debounce time.Duration, send func(reason string)) {
	var pending string
	var window <-chan time.Time
	for {
		select {
		case reason, ok := <-events:
			if !ok {
				return
			}
			if pending != eventWake {
				pending = reason
			}
			if window == nil {
				window = time.After(debounce)
			}
		case <-window:
			send(pending)
			pending, window = "", nil
		}
	}
}

func sendNetworkEvent(reason string) {
	log.Infof(desktopLogCategory, "system event: %s", reason)
	// The binding logs a failed call; an empty session ID means the process-owned session.
	callNetworkEvent(desktopProcessBinding(), reason)
}

func callNetworkEvent(binding *mobilebinding.Binding, reason string) string {
	params, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		return ""
	}
	return binding.CallJSON(context.Background(), "NetworkEvent", params)
}
