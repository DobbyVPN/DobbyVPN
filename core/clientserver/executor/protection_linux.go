//go:build linux && !android

package executor

import (
	"core/routing"
	"core/sessionapi"
)

var desktopProtection sessionapi.DesktopProtection = routing.LinuxServiceProtection{}
