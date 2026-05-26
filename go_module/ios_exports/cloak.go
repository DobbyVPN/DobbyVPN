package cloak_outline

import (
	"go_module/cloak"
	"go_module/log"
)

func StartCloakClient(localHost string, localPort string, config string, udp bool) (err error) {
	defer guardErr("StartCloakClient", &err)()
	log.Debugf(Category, "StartCloakClient begin localHost=%s localPort=%s configLen=%d udp=%v", localHost, localPort, len(config), udp)
	err = cloak.StartCloakClient(localHost, localPort, config, udp)
	if err != nil {
		log.Debugf(Category, "StartCloakClient failed localHost=%s localPort=%s err=%v", localHost, localPort, err)
		return err
	}
	log.Debugf(Category, "StartCloakClient OK localHost=%s localPort=%s", localHost, localPort)
	return nil
}

func StopCloakClient() {
	defer guard("StopCloakClient")()
	log.Debugf(Category, "StopCloakClient begin")
	cloak.StopCloakClient()
	log.Debugf(Category, "StopCloakClient returned")
}
