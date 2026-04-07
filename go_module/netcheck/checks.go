package netcheck

import (
	"context"
	"errors"
	"fmt"
	"go_module/log"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/checkers"
)

type NetCheckApp struct {
	ctx       context.Context
	cancel    context.CancelFunc
	interrupt chan bool
}

var InterruptedError error = errors.New("Check interrupted")
var NoInternetConnectionError error = errors.New("No internet connection")
var UserUnderWhitelistsError error = errors.New("User under whitelists")

func NewNetCheckApp() *NetCheckApp {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := make(chan bool, 1)

	return &NetCheckApp{ctx: ctx, cancel: cancel, interrupt: interrupt}
}

func (app *NetCheckApp) runWhoami() error {
	result, err := checkers.Whoami()

	log.Infof("[NETCHECK] === WHOAMI ===")
	if err != nil {
		return fmt.Errorf("Error running whoami check: %v", err)
	} else {
		log.Infof("[NETCHECK] IP      : %s", result.Ip)
		log.Infof("[NETCHECK] Subnet  : %s", result.Subnet)
		log.Infof("[NETCHECK] Asn     : %s", result.Asn)
		log.Infof("[NETCHECK] Org     : %s", result.Org)
		log.Infof("[NETCHECK] Location: %s", result.Location)

		return nil
	}
}

func (app *NetCheckApp) runCidrWhitelist() error {
	err := checkers.CidrWhitelist()

	log.Infof("[NETCHECK] === CIDRWHITELIST ===")
	if err != nil {
		switch err {
		case checkers.ErrCidrWhitelistDetected:
			log.Infof("[NETCHECK] You are under whitelist!")

			return UserUnderWhitelistsError
		case checkers.ErrCidrWhitelistNoInetAccess:
			log.Infof("[NETCHECK] You have no internet connection!")

			return NoInternetConnectionError
		default:
			return fmt.Errorf("Error running cidr whitelist check: %v", err)
		}
	} else {
		log.Infof("[NETCHECK] You are NOT under whitelist!")

		return nil
	}
}

func (app *NetCheckApp) runWebhost() error {
	runnerOpt := checkers.WebhostGochanRunnerOpt{Ctx: app.ctx, Mode: checkers.WebHostModePopular}
	out := checkers.WebhostGochanRunner(runnerOpt)

	log.Infof("[NETCHECK] === WEBHOST ===")
	for {
		select {
		case <-app.interrupt:
			log.Infof("[NETCHECK] WEBHOST check interrupted")

			return InterruptedError
		case msg1, ok := <-out.Out:
			log.Infof("[NETCHECK] IpInfo : %s:%d %s %s %s", msg1.Out.IpInfo.Ip, msg1.Out.IpInfo.Asn, msg1.Out.IpInfo.Subnet, msg1.Out.IpInfo.Org, msg1.Out.IpInfo.CountryIso)
			log.Infof("[NETCHECK] Port   : %d", msg1.Out.Port)
			log.Infof("[NETCHECK] TlsV   : %d", msg1.Out.TlsV)
			log.Infof("[NETCHECK] Sni    : %s", msg1.Out.Sni)
			log.Infof("[NETCHECK] Host   : %s", msg1.Out.Host)

			if msg1.Out.Alive != nil {
				log.Infof("[NETCHECK] Alive  : %v", msg1.Out.Alive)
			} else {
				log.Infof("[NETCHECK] Alive  : OK")
			}

			if msg1.Out.Tcp1620 != nil {
				log.Infof("[NETCHECK] Tcp1620: %v", msg1.Out.Tcp1620)
			} else {
				log.Infof("[NETCHECK] Tcp1620: OK")
			}

			log.Infof("[NETCHECK] ===============")

			if !ok {
				log.Infof("[NETCHECK] WEBHOST check completed")

				return nil
			}
		}
	}
}
