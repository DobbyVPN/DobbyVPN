//go:build !windows

package drivers

import (
	log "go_client/logger"
)

func AddTapDevice(appDir string) {
	log.Infof("No need to install TAP driver on non Windows OS")
}
