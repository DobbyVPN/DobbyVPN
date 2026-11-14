//go:build linux && !(android || ios)

package runtime

import (
	"core/log"
	"core/routing"
	"core/sessionapi"
)

// interruptedStateMessage is the only recovery text shown to clients; the
// detailed cause goes to the service log.
const interruptedStateMessage = "could not restore network state left by a previous run; the VPN was not started. See journalctl -u dobbyvpn for details"

// recoverOwnedLinuxRoutes is a seam for the runtime unit tests. The concrete
// implementation removes only routes carrying DobbyVPN's ownership tags.
var recoverOwnedLinuxRoutes = routing.RecoverLinuxOwnedRoutes

// RecoverInterruptedState reclaims product-owned Linux routing state left by
// an interrupted service process. It runs at service start and again before
// every Connect. A failure is returned as a PLATFORM_FAILED error with a short
// message; the caller must not create a tunnel while stale state remains.
func RecoverInterruptedState() error {
	if err := recoverOwnedLinuxRoutes(
		ownedRoutingTableID,
		ownedRoutingPriority,
		ownedTunName,
	); err != nil {
		log.Errorf(category, "recover interrupted Linux routing state: %v", err)
		return &sessionapi.Error{Code: sessionapi.FailurePlatform, Message: interruptedStateMessage}
	}
	return nil
}
