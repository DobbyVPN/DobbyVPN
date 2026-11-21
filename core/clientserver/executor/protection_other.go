//go:build !(linux || windows || ios || android)

package executor

import "core/sessionapi"

var desktopProtection sessionapi.DesktopProtection
