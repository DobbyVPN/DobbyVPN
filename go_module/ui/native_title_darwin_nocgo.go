//go:build darwin && !ios && !cgo

package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
)

func publishPlatformNativeWindowTitle(window fyne.Window, title string) error {
	return fmt.Errorf("macOS native window title publication requires cgo")
}
