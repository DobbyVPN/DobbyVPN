package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/checkers"
	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/config"
	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/updater"
)

type NetCheckApp struct {
	ctx       context.Context
	cancel    context.CancelFunc
	interrupt chan bool
}

var app *NetCheckApp = nil

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

func netCheckInternal() {
	log.Printf("[NETCHECK] netCheckInternal\n")

	app.runWhoami()
	app.runCidrWhitelist()
	app.runWebhost()
}

func NetCheck(configPath string) error {
	log.Printf("[NETCHECK] Loading config\n")
	if err := config.Load(configPath); err != nil {
		return fmt.Errorf("Error loading config: %v", err)
	}

	log.Printf("[NETCHECK] Geolite update\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	if err := updater.GeoliteUpdate(ctx); err != nil {
		cancel()
		return fmt.Errorf("Error run GeoliteUpdate: %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())

	app = &NetCheckApp{ctx: ctx, cancel: cancel, interrupt: make(chan bool, 1)}

	log.Printf("[NETCHECK] Start netcheck\n")
	go netCheckInternal()

	return nil
}

func CancelNetCheck() {
	log.Printf("[NETCHECK] Cancel netcheck\n")
	app.interrupt <- true
	app.cancel()

}

func main() {
	cfgPath := flag.String("cfg", "dpi-checkers/ru/dpi-ch/config/default.yaml", ".yaml config path")
	flag.Parse()

	NetCheck(*cfgPath)

	time.Sleep(time.Second * 2)

	CancelNetCheck()

	time.Sleep(time.Second * 2)
}
