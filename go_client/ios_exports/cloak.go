package cloak_outline

import (
    "go_client/cloak"
    "runtime/debug"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) {
    debug.SetMemoryLimit(30 << 20) // 45 MB
    debug.SetGCPercent(50)
    cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
    cloak.StopCloakClient()
}
