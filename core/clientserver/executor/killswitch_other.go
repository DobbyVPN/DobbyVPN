//go:build !darwin && !(android || ios)

package executor

import "context"

// Only macOS firewall rules outlive the backend process.
var releaseUnadoptedKillSwitch = func(context.Context) error { return nil }
