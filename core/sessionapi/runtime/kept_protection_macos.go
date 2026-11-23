//go:build darwin && !(android || ios)

package runtime

import (
	"context"

	"core/routing"
)

// ReleaseKeptProtection removes the macOS kill switch a network released for
// a rebuild kept, when no new network adopted it. The macOS kill switch
// outlives its network, so a Retry can rebuild the network behind it.
func (r *runtime) ReleaseKeptProtection(ctx context.Context) error {
	return routing.ReleaseUnadoptedMacOSKillSwitch(ctx)
}
