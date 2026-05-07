package cloak_outline

import (
	"go_module/cloak"
	"go_module/log"
	"time"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) {
	start := time.Now()
	log.Infof("[ios_exports] StartCloakClient begin localHost=%s localPort=%s config.len=%d udp=%v", localHost, localPort, len(config), udp)
	cloak.StartCloakClient(localHost, localPort, config, udp)
	log.Infof("[ios_exports] StartCloakClient returned elapsed=%s", time.Since(start))
}

func StopCloakClient() {
	start := time.Now()
	log.Infof("[ios_exports] StopCloakClient begin")
	cloak.StopCloakClient()
	log.Infof("[ios_exports] StopCloakClient returned elapsed=%s", time.Since(start))
}
