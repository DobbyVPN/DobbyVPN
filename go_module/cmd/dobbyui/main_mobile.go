//go:build android || ios

package main

import (
	"fyne.io/fyne/v2/app"

	"go_module/ui"
)

func main() {
	// The native Android/iOS shell owns VPN permission and transport. The Go
	// process renders only the shared UI and calls the platform transport
	// selected by its build target.
	ui.MarkStartup()
	ui.NewApplicationWithLogExporter(
		app.NewWithID("com.dobby.vpn"),
		ui.NewMobileClient(),
		ui.NewMobileLogExporter(),
	).Run()
}
