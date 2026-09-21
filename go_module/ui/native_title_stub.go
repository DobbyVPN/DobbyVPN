//go:build !darwin || ios

package ui

import "fyne.io/fyne/v2"

func publishPlatformNativeWindowTitle(window fyne.Window, title string) error { return nil }
