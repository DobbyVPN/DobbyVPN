//go:build darwin && !ios

package runtime

import "core/sessionapi"

func ProtectedRestoreLoader() sessionapi.ConfigLoader { return nil }
