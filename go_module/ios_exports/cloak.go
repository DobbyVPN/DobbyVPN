package cloak_outline

import (
	"go_module/cloak"
	"go_module/log"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) (err error) {
	defer guardErr("StartCloakClient", &err)()
	log.Infof("[ios_exports] StartCloakClient begin localHost=%s localPort=%s configLen=%d udp=%v", localHost, localPort, len(config), udp)
	err = cloak.StartCloakClient(localHost, localPort, config, udp)
	if err != nil {
		log.Infof("[ios_exports] StartCloakClient failed localHost=%s localPort=%s err=%v", localHost, localPort, err)
		return err
	}
	log.Infof("[ios_exports] StartCloakClient OK localHost=%s localPort=%s", localHost, localPort)
	return nil
}

func StopCloakClient() {
	defer guard("StopCloakClient")()
	log.Infof("[ios_exports] StopCloakClient begin")
	cloak.StopCloakClient()
	log.Infof("[ios_exports] StopCloakClient returned")
}
