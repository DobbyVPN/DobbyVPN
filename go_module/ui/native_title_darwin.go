//go:build darwin && !ios && cgo

package ui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdint.h>
#include <stdlib.h>

int dobby_set_macos_window_title(uintptr_t window, const char *title);
*/
import "C"

import (
	"fmt"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

// publishPlatformNativeWindowTitle updates the actual NSWindow obtained from
// Fyne's public NativeWindow contract. This is deliberately product-owned
// AppKit glue: it does not replace, patch, vendor, or fork Fyne/GLFW. The
// direct title assignment and accessibility notification make a dynamic
// status observable to macOS AX clients even when Fyne's custom child
// accessibility elements retain their initial snapshot metadata.
func publishPlatformNativeWindowTitle(window fyne.Window, title string) error {
	native, ok := window.(driver.NativeWindow)
	if !ok {
		return fmt.Errorf("Fyne window does not implement driver.NativeWindow")
	}
	var publicationError error
	native.RunNative(func(value any) {
		context, ok := value.(driver.MacWindowContext)
		if !ok {
			publicationError = fmt.Errorf("Fyne native context is %T, want driver.MacWindowContext", value)
			return
		}
		if context.NSWindow == 0 {
			// Before Show Fyne has no NSWindow yet. The caller installs the
			// ordinary title before Show and invokes this bridge only after it.
			return
		}
		cTitle := C.CString(title)
		defer C.free(unsafe.Pointer(cTitle))
		switch status := int(C.dobby_set_macos_window_title(C.uintptr_t(context.NSWindow), cTitle)); status {
		case 0:
			return
		case 1:
			publicationError = fmt.Errorf("native NSWindow handle is unavailable")
		case 2:
			publicationError = fmt.Errorf("native title is unavailable")
		case 3:
			publicationError = fmt.Errorf("native title publication ran off the AppKit main thread")
		case 4:
			publicationError = fmt.Errorf("NSWindow title readback did not match requested title")
		case 5:
			publicationError = fmt.Errorf("NSWindow accessibility title readback did not match requested title")
		default:
			publicationError = fmt.Errorf("native AppKit title publication failed (status %d)", status)
		}
	})
	return publicationError
}
