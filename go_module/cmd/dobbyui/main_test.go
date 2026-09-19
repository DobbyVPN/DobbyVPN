//go:build !(android || ios)

package main

import (
	"testing"

	"fyne.io/fyne/v2/test"
)

func TestDesktopApplicationInjectsLogExporter(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	application := newDesktopApplication(runtime, nil, nil)
	if application.Connection.Export.Disabled() {
		t.Fatal("desktop production entrypoint left log export disabled")
	}
}
