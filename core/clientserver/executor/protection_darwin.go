//go:build darwin && !ios

package executor

import (
	"core/sessionapi"
	"core/sessionapi/macosprovider"
)

var macDesktop macosprovider.Desktop
var desktopProtection sessionapi.DesktopProtection = &macDesktop

func desktopRuntime() sessionapi.Runtime { return &macDesktop }
