package outlinebridge

import (
	"go_client/cloak"
)

func StartCloakClient(localHostC  *C.char, localPortC  *C.char, configC  *C.char, udp bool) {
	localHost := C.GoString(localHostC)
	localPort := C.GoString(localPortC)
	config := C.GoString(configC)
	cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
