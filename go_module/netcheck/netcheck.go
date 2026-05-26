package netcheck

import (
	"context"
	"fmt"
	"go_module/log"
	"sync"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/config"
	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/updater"
)

var app *NetCheckApp = nil
var appMu sync.Mutex

func (a *NetCheckApp) netCheckInternal() {
	defer func() {
		appMu.Lock()
		app = nil
		appMu.Unlock()
	}()

	log.Debugf(Category, "Geolite update", make(map[string]string))
	ctx, cancel := context.WithCancel(a.ctx)
	if err := updater.GeoliteUpdate(ctx); err != nil {
		cancel()
		log.Debugf(Category, "Error run GeoliteUpdate", map[string]string{"err": err.Error()})
		return
	}

	log.Debugf(Category, "Net check: start", make(map[string]string))

	if err := a.runWhoami(); err != nil {
		log.Debugf(Category, "Error running whoami, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}
	if err := a.runCidrWhitelist(); err != nil {
		log.Debugf(Category, "Error running cidrwhitelist, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}
	if err := a.runDns(); err != nil {
		log.Debugf(Category, "Error running dns, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}
	if err := a.runWebhost(); err != nil {
		log.Debugf(Category, "Error running webhost, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}

	log.Debugf(Category, "Net check: completed", make(map[string]string))
}

func NetCheck(configPath string) error {
	log.Debugf(Category, "Loading config", make(map[string]string))
	if err := config.Load(configPath); err != nil {
		log.Debugf(Category, "Error loading config", map[string]string{"err": err.Error()})
	}

	appMu.Lock()
	defer appMu.Unlock()

	if app == nil {
		log.Debugf(Category, "Creating new app", make(map[string]string))
		app = NewNetCheckApp()

		log.Debugf(Category, "Running app", make(map[string]string))
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
		log.Debugf(Category, "Cancel netcheck", make(map[string]string))
		app.interrupt <- true
		app.cancel()
		app = nil
	} else {
		log.Debugf(Category, "No need to cancel netcheck", make(map[string]string))
	}
}
