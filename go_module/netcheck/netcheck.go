package netcheck

import (
	"context"
	"fmt"
	"go_module/log"
	"sync"
	"time"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/config"
	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/updater"
)

var app *NetCheckApp = nil
var appMu sync.Mutex

func (app *NetCheckApp) netCheckInternal() {
	log.Infof("[NETCHECK] Net check: start")

	var err error

	if err = app.runWhoami(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running whoami, interrupting netcheck: %v", err)
		return
	}
	if err = app.runCidrWhitelist(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running cidrwhitelist, interrupting netcheck: %v", err)
		return
	}
	if err = app.runWebhost(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running webhost, interrupting netcheck: %v", err)
		return
	}

	log.Infof("[NETCHECK] Net check: completed")

	appMu.Lock()
	app = nil
	appMu.Unlock()
}

func NetCheck(configPath string) error {
	log.Infof("[NETCHECK] Loading config")
	if err := config.Load(configPath); err != nil {
		return fmt.Errorf("Error loading config: %v", err)
	}

	log.Infof("[NETCHECK] Geolite update")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	if err := updater.GeoliteUpdate(ctx); err != nil {
		cancel()
		return fmt.Errorf("Error run GeoliteUpdate: %v", err)
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
		go func() {
			app.interrupt <- true
			app.cancel()
		}()
	} else {
		log.Infof("[NETCHECK] No need to cancel netcheck")
	}
	app = nil
}
