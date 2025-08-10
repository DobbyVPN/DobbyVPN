package cloak_outline

import (
    "fmt"
    "go_client/cloak"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) {
    fmt.Println(localHost + localPort + config)
    fmt.Println(udp)
    // Когда будет готова функция cloak.StartCloakClient, вызываем её:
    // cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
    cloak.StopCloakClient()
}
