package vpnmanager

import (
	"time"

	"go_module/cloak"
	"go_module/log"
)

func StartCloakClient(category, localHost, localPort, config string, udp bool) error {
	start := time.Now()
	if category != "" {
		log.Debugf(category, "StartCloakClient begin localHost=%s localPort=%s config.len=%d udp=%v", localHost, localPort, len(config), udp)
	}
	err := cloak.StartCloakClient(localHost, localPort, config, udp)
	if err != nil {
		if category != "" {
			log.Debugf(category, "StartCloakClient failed localHost=%s localPort=%s elapsed=%s err=%v", localHost, localPort, time.Since(start), err)
		}
		return err
	}
	if category != "" {
		log.Debugf(category, "StartCloakClient returned elapsed=%s", time.Since(start))
	}
	return nil
}

func StopCloakClient(category string) {
	start := time.Now()
	if category != "" {
		log.Debugf(category, "StopCloakClient begin")
	}
	cloak.StopCloakClient()
	if category != "" {
		log.Debugf(category, "StopCloakClient returned elapsed=%s", time.Since(start))
	}
}
