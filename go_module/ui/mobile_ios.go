//go:build ios

package ui

/*
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

// package_ios_app.sh stages the matching CommonDI.framework here before
// Fyne invokes the iOS cross-compiler. Using a cgo directive keeps the link
// flags in the Go package itself; Fyne's mobile builder intentionally replaces
// the caller's process environment while constructing its per-architecture
// toolchain, so an exported CGO_LDFLAGS value would be lost.
#cgo ios LDFLAGS: -F${SRCDIR}/../native-ios -framework CommonDI

char* dobby_ui_configure(const char*, int64_t, const uint8_t*, int);
char* dobby_ui_start(const char*, int64_t, const char*, int32_t);
char* dobby_ui_stop(const char*, int64_t);
char* dobby_ui_snapshot(const char*);
char* dobby_ui_reset(const char*, int64_t);
void dobby_ui_free_string(char*);
void dobby_ui_startup(const char*);
void dobby_ui_attached(void);
*/
import "C"

import "unsafe"

// iosTransport forwards every UI operation to the containing Swift shell.
// The Swift shell owns NetworkExtension; this process never constructs a Go
// session manager or links the Packet Tunnel runtime.
type iosTransport struct{}

func markNativeStartup() {
	mode := C.CString("normal")
	defer C.free(unsafe.Pointer(mode))
	C.dobby_ui_startup(mode)
}

func markNativeUIAttached() { C.dobby_ui_attached() }

func newMobileAPI() mobileAPI {
	transport := iosTransport{}
	return mobileAPI{
		configure: transport.Configure,
		start:     transport.Start,
		stop:      transport.Stop,
		snapshot:  transport.Snapshot,
		reset:     transport.Reset,
	}
}

func cString(value string) *C.char { return C.CString(value) }

func takeCString(value *C.char) string {
	if value == nil {
		return ""
	}
	defer C.dobby_ui_free_string(value)
	return C.GoString(value)
}

func (iosTransport) Configure(session string, sequence int64, raw []byte) string {
	csession := cString(session)
	defer C.free(unsafe.Pointer(csession))
	var data *C.uint8_t
	if len(raw) > 0 {
		data = (*C.uint8_t)(unsafe.Pointer(&raw[0]))
	}
	return takeCString(C.dobby_ui_configure(csession, C.int64_t(sequence), data, C.int(len(raw))))
}

func (iosTransport) Start(session string, sequence int64, mode string, index int32) string {
	csession := cString(session)
	cmode := cString(mode)
	defer C.free(unsafe.Pointer(csession))
	defer C.free(unsafe.Pointer(cmode))
	return takeCString(C.dobby_ui_start(csession, C.int64_t(sequence), cmode, C.int32_t(index)))
}

func (iosTransport) Stop(session string, generation int64) string {
	csession := cString(session)
	defer C.free(unsafe.Pointer(csession))
	return takeCString(C.dobby_ui_stop(csession, C.int64_t(generation)))
}

func (iosTransport) Snapshot(session string) string {
	csession := cString(session)
	defer C.free(unsafe.Pointer(csession))
	return takeCString(C.dobby_ui_snapshot(csession))
}

func (iosTransport) Reset(session string, sequence int64) string {
	csession := cString(session)
	defer C.free(unsafe.Pointer(csession))
	return takeCString(C.dobby_ui_reset(csession, C.int64_t(sequence)))
}
