//go:build !windows && !linux && !darwin && !(android || ios)

package internal

import (
	"fmt"
	"go_module/core/pkg"
)

func (app *App) SwitchProtocolDevice(device pkg.ProtocolDevice) error {
	return fmt.Errorf("desktop protocol hot-switch is not supported on this platform")
}
