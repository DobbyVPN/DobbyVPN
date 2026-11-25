//go:build darwin && !ios

package executor

import "core/routing"

var releaseUnadoptedKillSwitch = macDesktop.ReleaseIdleLegacyGuard
var releaseLegacyInstallationGuard = routing.ReleaseLegacyMacOSGuard
