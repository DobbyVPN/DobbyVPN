//go:build android

package ui

import dobbyvpn "go_module/kotlin_exports"

func newMobileAPI() mobileAPI {
	return mobileAPI{
		configure: dobbyvpn.ConfigureSession,
		start:     dobbyvpn.StartSession,
		stop:      dobbyvpn.StopSession,
		snapshot:  dobbyvpn.SnapshotSession,
		reset:     dobbyvpn.ResetSession,
	}
}
