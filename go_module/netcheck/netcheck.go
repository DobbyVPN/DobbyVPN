package netcheck

import (
	"context"
	"fmt"
	"go_module/log"
	"sync"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/config"
	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/updater"
)

const HASH_POSTFIX = ".hash"

var app *NetCheckApp = nil
var appMu sync.Mutex

func (a *NetCheckApp) netCheckInternal() {
	defer func() {
		appMu.Lock()
		app = nil
		appMu.Unlock()
	}()

	log.Infof("[NETCHECK] Geolite update")
	ctx, cancel := context.WithCancel(a.ctx)
	if err := updater.GeoliteUpdate(ctx); err != nil {
		cancel()
		log.Infof("[NETCHECK] [ERROR] Error run GeoliteUpdate: %v", err)
		return
	}

	log.Infof("[NETCHECK] Net check: start")

	if err := a.runWhoami(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running whoami, interrupting netcheck: %v", err)
		return
	}
	if err := a.runCidrWhitelist(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running cidrwhitelist, interrupting netcheck: %v", err)
		return
	}
	if err := a.runDns(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running dns, interrupting netcheck: %v", err)
		return
	}
	if err := a.runWebhost(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running webhost, interrupting netcheck: %v", err)
		return
	}

	log.Infof("[NETCHECK] Net check: completed")
}

func NetCheck(configPath string) error {
	log.Infof("[NETCHECK] Loading config")
	if err := config.Load(configPath); err != nil {
		return fmt.Errorf("Error loading config: %v", err)
	}

	appMu.Lock()
	defer appMu.Unlock()

	if app == nil {
		log.Infof("[NETCHECK] Creating new app")
		app = NewNetCheckApp()

		log.Infof("[NETCHECK] Running app")
		go app.netCheckInternal()

		return nil
	} else {
		return fmt.Errorf("App is running")
	}
}

func CancelNetCheck() {
	appMu.Lock()
	defer appMu.Unlock()
	if app != nil {
		log.Infof("[NETCHECK] Cancel netcheck")
		app.interrupt <- true
		app.cancel()
		app = nil
	} else {
		log.Infof("[NETCHECK] No need to cancel netcheck")
	}
}
