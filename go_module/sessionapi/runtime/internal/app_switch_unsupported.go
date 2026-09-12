//go:build !windows && !linux && !darwin && !(android || ios)

package internal

import (
	"fmt"
	"go_module/log"
	"go_module/protocol"
)

func (app *App) SwitchProtocolDevice(device protocol.ProtocolDevice) error {
	if device != nil {
		if err := device.Close(); err != nil {
			log.Debugf(Category, "Failed to close replacement ProtocolDevice after unsupported hot-switch: %v", err)
		}
	}
	return fmt.Errorf("desktop protocol hot-switch is not supported on this platform")
}
