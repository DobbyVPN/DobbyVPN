//go:build linux && !(android || ios)

package runtime

import (
	"fmt"

	"core/routing"
)

// recoverOwnedLinuxRoutes is a seam for the runtime unit tests. The concrete
// implementation removes only routes carrying DobbyVPN's ownership tags.
var recoverOwnedLinuxRoutes = routing.RecoverLinuxOwnedRoutes

// recoverOwnedLinuxNetworkRules is the same kind of seam for the retained-network
// policy rules, which the route recovery above does not recognize.
var recoverOwnedLinuxNetworkRules = routing.RecoverLinuxNetworkRules

// RecoverInterruptedState reclaims product-owned Linux routing state left by
// an interrupted service process. It is called before the desktop control
// socket is advertised, so a failed recovery keeps the service fail-closed
// instead of exposing an apparently disconnected process with stale routes.
func RecoverInterruptedState() error {
	if err := recoverOwnedLinuxNetworkRules(
		ownedRoutingTableID,
		ownedRoutingPriority,
		ownedTunName,
	); err != nil {
		return fmt.Errorf("recover interrupted Linux routing state: %w", err)
	}
	if err := recoverOwnedLinuxRoutes(
		ownedRoutingTableID,
		ownedRoutingPriority,
		ownedTunName,
	); err != nil {
		return fmt.Errorf("recover interrupted Linux routing state: %w", err)
	}
	return nil
}
