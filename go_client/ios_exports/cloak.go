package cloak_outline

import (
    "fmt"
    "go_client/cloak"
)

func StartCloakClient(localHost, localPort, config *string, udp bool) {
    // Чтобы вывести значения строк, нужно разыменовать указатели
    fmt.Println(*localHost + *localPort + *config)
    fmt.Println(udp)
    // Раскомментируй, когда cloak.StartCloakClient готов к использованию
    // cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
    cloak.StopCloakClient()
}
