//go:build android || ios

package main

import (
	"fyne.io/fyne/v2/app"

	"go_module/ui"
)

func main() {
	// The native Android/iOS shell owns VPN permission and transport. The Go
	// process renders only the shared UI and calls the platform transport
	// selected by its build target. The diagnostic store is resolved by that
	// shell so this entrypoint never guesses a HOME/App Group path. Resolve it
	// after the native window is shown: the Android bridge needs the live Fyne
	// activity context and must not be on the first-render path.
	runtime := app.NewWithID("com.dobby.vpn")
	ui.MarkStartup()
	ui.NewApplicationWithLogExporter(
		runtime,
		ui.NewMobileClient(),
		ui.NewMobileLogExporter(),
	).RunWithDiagnosticStore(ui.NewMobileDiagnosticStore)
}
