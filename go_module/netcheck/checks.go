package main

import (
	"context"
	"log"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/checkers"
)

type NetCheckApp struct {
	ctx       context.Context
	cancel    context.CancelFunc
	interrupt chan bool
}

func NewNetCheckApp() *NetCheckApp {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := make(chan bool, 1)

	return &NetCheckApp{ctx: ctx, cancel: cancel, interrupt: interrupt}
}

func (app *NetCheckApp) runWhoami() {
	result, err := checkers.Whoami()

	log.Printf("[NETCHECK] === WHOAMI ===\n")
	if err != nil {
		log.Printf("[NETCHECK] Failed run whoami test: %v\n", err)
	} else {
		log.Printf("[NETCHECK] IP		: %s:\n", result.Ip)
		log.Printf("[NETCHECK] Subnet	: %s:\n", result.Subnet)
		log.Printf("[NETCHECK] Asn		: %s\n", result.Asn)
		log.Printf("[NETCHECK] Org		: %s\n", result.Org)
		log.Printf("[NETCHECK] Location	: %s:\n", result.Location)
	}
}

func (app *NetCheckApp) runCidrWhitelist() {
	err := checkers.CidrWhitelist()

	log.Printf("[NETCHECK] === CIDRWHITELIST ===\n")
	if err != nil {
		switch err {
		case checkers.ErrCidrWhitelistDetected:
			log.Printf("[NETCHECK] You are under whitelist!\n")
		case checkers.ErrCidrWhitelistNoInetAccess:
			log.Printf("[NETCHECK] You have no internet connection!\n")
		default:
			log.Printf("[NETCHECK] [ERROR] Error running test: %v\n", err)
		}
	} else {
		log.Printf("[NETCHECK] You are NOT under whitelist!")
	}
}

func (app *NetCheckApp) runWebhost() {
	runnerOpt := checkers.WebhostGochanRunnerOpt{Ctx: app.ctx, Mode: checkers.WebHostModePopular}
	out := checkers.WebhostGochanRunner(runnerOpt)

	log.Printf("[NETCHECK] === WEBHOST ===\n")
	for {
		select {
		case <-app.interrupt:
			log.Printf("[NETCHECK] WEBHOST check interrupted\n")

			return
		case msg1, ok := <-out.Out:
			log.Printf("[NETCHECK] IpInfo	: %s:%d %s %s %s\n", msg1.Out.IpInfo.Ip, msg1.Out.IpInfo.Asn, msg1.Out.IpInfo.Subnet, msg1.Out.IpInfo.Org, msg1.Out.IpInfo.CountryIso)
			log.Printf("[NETCHECK] Port		: %d\n", msg1.Out.Port)
			log.Printf("[NETCHECK] TlsV		: %d\n", msg1.Out.TlsV)
			log.Printf("[NETCHECK] Sni		: %s\n", msg1.Out.Sni)
			log.Printf("[NETCHECK] Host		: %s\n", msg1.Out.Host)
			if msg1.Out.Alive != nil {
				log.Printf("[NETCHECK] Alive	: %v\n", msg1.Out.Alive)
			} else {
				log.Printf("[NETCHECK] Alive	: OK\n")
			}
			if msg1.Out.Tcp1620 != nil {
				log.Printf("[NETCHECK] Tcp1620	: %v\n", msg1.Out.Tcp1620)
			} else {
				log.Printf("[NETCHECK] Tcp1620	: OK\n")
			}
			log.Printf("[NETCHECK] ===============\n")

			if !ok {
				log.Printf("[NETCHECK] WEBHOST check completed\n")

				return
			}
		}
	}
}
