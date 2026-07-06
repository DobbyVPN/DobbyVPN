//go:build !windows && !linux && !darwin && !(android || ios)

package internal

import (
	"fmt"
	coreCommon "go_module/core/common"
	"go_module/core/pkg"
	"go_module/log"
)

func (app *App) SwitchProtocolDevice(device pkg.ProtocolDevice) error {
	if device != nil {
		if err := device.Close(); err != nil {
			log.Debugf(coreCommon.Category, "Failed to close replacement ProtocolDevice after unsupported hot-switch: %v", err)
		}
	}
	return fmt.Errorf("desktop protocol hot-switch is not supported on this platform")
}
