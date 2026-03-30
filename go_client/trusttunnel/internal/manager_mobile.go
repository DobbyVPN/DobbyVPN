//go:build android || ios
// +build android ios

package internal

/*
#cgo CFLAGS: -I${SRCDIR}/dobby_bridge

// Android: Link the static library and Android's native logging/networking
#cgo android LDFLAGS: -L${SRCDIR}/lib/android/arm64-v8a -ldobby_bridge -llog -lm -lc++_shared

// iOS: Link the static library and Apple's Network Extension frameworks
#cgo ios LDFLAGS: -L${SRCDIR}/lib/ios -ldobby_bridge -framework Foundation -framework NetworkExtension

#include <stdlib.h>
#include "dobby_bridge/dobby_bridge.h"

// 1. Define the C-Callback type
typedef int (*protect_cb_t)(int fd);

// 2. Helper to execute the callback safely in C
static inline int execute_protect_cb(protect_cb_t cb, int fd) {
    if (cb == NULL) return 0;
    return cb(fd);
}

// C "Gateway" functions
extern void c_state_changed_cb(void* arg, int state);
extern void c_log_cb(int level, const char* msg);
extern int c_protect_cb(int fd);
*/
import "C"
import (
	log "go_client/logger"
	"unsafe"
)

// Global reference to the Kotlin callback pointer
var globalProtectCallback C.protect_cb_t

// SetMobileProtectCallback is called from your exported wrapper
func SetMobileProtectCallback(ptr unsafe.Pointer) {
	globalProtectCallback = (C.protect_cb_t)(ptr)
}

//export go_protect_socket
func go_protect_socket(fd C.int) C.int {
	if globalProtectCallback != nil {
		// Call the Kotlin function.
		// Kotlin will return 1 for True (Protected), 0 for False.
		res := C.execute_protect_cb(globalProtectCallback, fd)
		if res == 1 {
			return 0 // TrustTunnel C++ expects 0 for Success
		}
		return -1 // TrustTunnel C++ expects -1 for Failure
	}
	return 0 // Default allow
}

//export go_state_changed
func go_state_changed(arg unsafe.Pointer, state C.int) {
	log.Infof("[TrustTunnel Mobile] State changed to: %d", int(state))
}

//export go_log_message
func go_log_message(level C.int, msg *C.char) {
	goMsg := C.GoString(msg)

	switch int(level) {
	case 0:
		log.Errorf("[TrustTunnel Core] %s", goMsg)
	case 1:
		log.Warnf("[TrustTunnel Core] %s", goMsg)
	case 3, 4:
		log.Debugf("[TrustTunnel Core] %s", goMsg)
	default:
		log.Infof("[TrustTunnel Core] %s", goMsg)
	}
}

type TrustTunnelManager struct {
	tomlConfig string
	fd         int
}

func NewTrustTunnelManager(tomlConfig string, fd int) *TrustTunnelManager {
	return &TrustTunnelManager{
		tomlConfig: tomlConfig,
		fd:         fd,
	}
}

func (m *TrustTunnelManager) Start() error {
	// Register the global mobile callbacks
	C.dobby_vpn_set_log_callback((C.dobby_on_log_message_t)(C.c_log_cb))
	C.dobby_vpn_set_protect_callback((C.dobby_on_protect_socket_t)(C.c_protect_cb))

	cConfig := C.CString(m.tomlConfig)
	defer C.free(unsafe.Pointer(cConfig))

	C.dobby_vpn_start(cConfig, (C.dobby_on_state_changed_t)(C.c_state_changed_cb), nil)
	return nil
}

func (m *TrustTunnelManager) Stop() {
	C.dobby_vpn_stop()
}
