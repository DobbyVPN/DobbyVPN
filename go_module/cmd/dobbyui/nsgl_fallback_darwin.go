//go:build darwin && !ios && cgo

package main

/*
#cgo LDFLAGS: -framework AppKit
int dobby_install_nsgl_software_fallback(void);
*/
import "C"

import "fmt"

func installNSGLSoftwareFallback() error {
	switch status := int(C.dobby_install_nsgl_software_fallback()); status {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("NSOpenGLPixelFormat initWithAttributes method is unavailable")
	default:
		return fmt.Errorf("could not install NSGL software fallback (status %d)", status)
	}
}
