//go:build linux && !android

package executor

import (
	"core/sessionapi"
	"core/sessionapi/runtime"
)

var desktopProtection sessionapi.DesktopProtection = runtime.LinuxServiceProtection()
