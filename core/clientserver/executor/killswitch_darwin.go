//go:build darwin && !ios

package executor

import "core/routing"

var releaseUnadoptedKillSwitch = routing.ReleaseUnadoptedMacOSKillSwitch
