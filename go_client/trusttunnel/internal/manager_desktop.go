//go:build !(android || ios)
// +build !android,!ios

package internal

/*
#cgo CFLAGS: -I${SRCDIR}/dobby_bridge

// --- OS-Specific Linking ---
// Windows expects a file named dobby_bridge.dll and dobby_bridge.lib in the same directory
#cgo windows LDFLAGS: -L${SRCDIR}/lib/windows -ldobby_bridge

// Linux expects a file named dobby_bridge.so
#cgo linux LDFLAGS: -L${SRCDIR}/lib/linux -ldobby_bridge -lpthread -ldl -lc++ -lc++abi -lm

// macOS expects a file named dobby_bridge.dylib
#cgo darwin LDFLAGS: -L${SRCDIR}/lib/macos -ldobby_bridge -framework CoreFoundation -framework Security

#include <stdlib.h>
#include "dobby_bridge/dobby_bridge.h"

extern void c_state_changed(void* arg, int state);
extern void c_log_message(int level, const char* msg);
*/
import "C"
import (
	log "go_client/logger"
	"unsafe"
)

//export go_state_changed
func go_state_changed(arg unsafe.Pointer, state C.int) {
	log.Infof("[TrustTunnel] State changed to: %d", int(state))
}

//export go_log_message
func go_log_message(level C.int, msg *C.char) {
	goMsg := C.GoString(msg)

	// ag::LogLevel mapping from logger.h: 0=ERROR, 1=WARN, 2=INFO, 3=DEBUG, 4=TRACE
	switch int(level) {
	case 0:
		log.Errorf("[TrustTunnel Core] %s", goMsg)
	case 1:
		log.Warnf("[TrustTunnel Core] %s", goMsg)
	case 2:
		log.Infof("[TrustTunnel Core] %s", goMsg)
	case 3, 4:
		log.Debugf("[TrustTunnel Core] %s", goMsg)
	default:
		log.Infof("[TrustTunnel Core] %s", goMsg)
	}
}

//export go_protect_socket
func go_protect_socket(fd C.int) C.int {
	return 0
}

type TrustTunnelManager struct {
	tomlConfig string
}

func NewTrustTunnelManager(tomlConfig string) *TrustTunnelManager {
	return &TrustTunnelManager{
		tomlConfig: tomlConfig,
	}
}

// Start launches the VPN with the given TOML configuration.
func (m *TrustTunnelManager) Start() error {
	// Hook the logger BEFORE starting the engine
	C.dobby_vpn_set_log_callback((C.dobby_on_log_message_t)(C.c_log_message))

	cConfig := C.CString(m.tomlConfig)
	defer C.free(unsafe.Pointer(cConfig))

	// Pass the TOML string, the bridged C callback, and a nil pointer for the argument
	C.dobby_vpn_start(cConfig, (C.dobby_on_state_changed_t)(C.c_state_changed), nil)
	return nil
}

// Stop halts the VPN and frees resources.
func (m *TrustTunnelManager) Stop() {
	C.dobby_vpn_stop()
}
