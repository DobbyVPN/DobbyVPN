//go:build !darwin && !android && !ios

package executor

import (
	"core/sessionapi"
	"core/sessionapi/runtimebridge"
)

func desktopRuntime() sessionapi.Runtime { return runtimebridge.New(nil) }
