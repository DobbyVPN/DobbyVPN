package cloak_outline

import (
    "fmt"
    "go_client/cloak"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) {
    cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
    cloak.StopCloakClient()
}
