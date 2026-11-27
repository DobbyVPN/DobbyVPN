//go:build !(linux || darwin || windows || ios || android)

package executor

import "core/sessionapi"

var desktopProtection sessionapi.DesktopProtection
