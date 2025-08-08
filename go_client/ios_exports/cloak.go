package outlinebridge

import (
    "fmt"
	"go_client/cloak"
)

func StartCloakClient(localHost  *string, localPort  *string, config  *string, udp bool) {
    fmt.println(localHost + localPort + config)
    fmt.println(udp)
// 	cloak.StartCloakClient(localHost, localPort, config, udp)
}

func StopCloakClient() {
	cloak.StopCloakClient()
}
