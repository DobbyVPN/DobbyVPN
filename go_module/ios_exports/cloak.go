//go:build ios

package cloak_outline

import (
	"fmt"
	"go_module/cloak"
	"go_module/log"
	"time"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) (err error) {
	defer guardErr("StartCloakClient", &err)()
	start := time.Now()
	log.Infof("[ios_exports] StartCloakClient begin localHost=%s localPort=%s config.len=%d udp=%v", localHost, localPort, len(config), udp)
	err = cloak.StartCloakClient(localHost, localPort, config, udp)
	if err != nil {
		log.Infof("[ios_exports] StartCloakClient failed localHost=%s localPort=%s elapsed=%s err=%v", localHost, localPort, time.Since(start), err)
		return fmt.Errorf("StartCloakClient failed: %w", err)
	}
	log.Infof("[ios_exports] StartCloakClient returned elapsed=%s", time.Since(start))
	return nil
}

func StopCloakClient() {
	defer guard("StopCloakClient")()
	start := time.Now()
	log.Infof("[ios_exports] StopCloakClient begin")
	cloak.StopCloakClient()
	log.Infof("[ios_exports] StopCloakClient returned elapsed=%s", time.Since(start))
}
