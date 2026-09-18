//go:build android || ios

package main

import (
	"fyne.io/fyne/v2/app"

	"go_module/ui"
)

func main() {
	ui.NewApplication(app.NewWithID("com.dobby.vpn"), ui.NewMobileClient()).Run()
}
