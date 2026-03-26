//go:build android || ios

package trusttunnel

/*
#cgo CFLAGS: -I${SRCDIR}/dobby_bridge

// Android: Link the static library and Android's native logging/networking
#cgo android LDFLAGS: -L${SRCDIR}/lib/android/arm64-v8a -ldobby_bridge -llog -lm -lc++_shared

// iOS: Link the static library and Apple's Network Extension frameworks
#cgo ios LDFLAGS: -L${SRCDIR}/lib/ios -ldobby_bridge -framework Foundation -framework NetworkExtension

#include <stdlib.h>
#include "dobby_bridge/dobby_bridge.h"

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

// SocketProtector is an interface that will be implemented in Kotlin/Swift.
type SocketProtector interface {
	Protect(fd int) bool
}

// Global reference to the active mobile protector
var activeProtector SocketProtector

// SetSocketProtector is called from Kotlin/Swift to inject the OS-level protector
func SetSocketProtector(protector SocketProtector) {
	activeProtector = protector
}

//export go_protect_socket
func go_protect_socket(fd C.int) C.int {
	// iOS NetworkExtension automatically handles routing, so it usually doesn't need this.
	// On Android, we MUST call VpnService.protect() via the injected interface.
	if activeProtector != nil {
		if activeProtector.Protect(int(fd)) {
			return 0 // 0 = Success (Socket Protected)
		}
		return -1 // -1 = Failed to protect
	}

	// Default to allow if no protector is registered
	return 0
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

type TrustTunnelManager struct{}

func NewTrustTunnelManager() *TrustTunnelManager {
	return &TrustTunnelManager{}
}

func (m *TrustTunnelManager) Start(tomlConfig string) error {
	// Register the global mobile callbacks
	C.dobby_vpn_set_log_callback((C.dobby_on_log_message_t)(C.c_log_cb))
	C.dobby_vpn_set_protect_callback((C.dobby_on_protect_socket_t)(C.c_protect_cb))

	cConfig := C.CString(tomlConfig)
	defer C.free(unsafe.Pointer(cConfig))

	C.dobby_vpn_start(cConfig, (C.dobby_on_state_changed_t)(C.c_state_changed_cb), nil)
	return nil
}

func (m *TrustTunnelManager) Stop() {
	C.dobby_vpn_stop()
}
