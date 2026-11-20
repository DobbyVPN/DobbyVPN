//go:build windows && (amd64 || arm64)

package executor

import (
	"core/routing"
	"core/sessionapi"
)

var desktopProtection sessionapi.DesktopProtection = routing.ServiceWindowsProtection
