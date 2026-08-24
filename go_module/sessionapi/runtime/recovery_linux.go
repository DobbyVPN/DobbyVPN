//go:build linux && !(android || ios)

package runtime

import (
	"fmt"

	"go_module/routing"
)

// recoverOwnedLinuxRoutes is a seam for the runtime unit tests. The concrete
// implementation removes only routes carrying DobbyVPN's ownership tags.
var recoverOwnedLinuxRoutes = routing.RecoverLinuxOwnedRoutes

// RecoverInterruptedState reclaims product-owned Linux routing state left by
// an interrupted service process. It is called before the desktop control
// socket is advertised, so a failed recovery keeps the service fail-closed
// instead of exposing an apparently disconnected process with stale routes.
func RecoverInterruptedState() error {
	if err := recoverOwnedLinuxRoutes(
		ownedRoutingTableID,
		ownedRoutingPriority,
		ownedTunName,
	); err != nil {
		return fmt.Errorf("recover interrupted Linux routing state: %w", err)
	}
	return nil
}
