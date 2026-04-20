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
var netcheckLogger log.ISubLogger = log.GetLogger().NewSubLogger("NCH")

func (a *NetCheckApp) netCheckInternal() {
	defer func() {
		appMu.Lock()
		app = nil
		appMu.Unlock()
	}()

	netcheckLogger.Debug("Geolite update", make(map[string]string))
	ctx, cancel := context.WithCancel(a.ctx)
	if err := updater.GeoliteUpdate(ctx); err != nil {
		cancel()
		netcheckLogger.Error("Error run GeoliteUpdate", map[string]string{"err": err.Error()})
		return
	}

	netcheckLogger.Debug("Net check: start", make(map[string]string))

	if err := a.runWhoami(); err != nil {
		netcheckLogger.Error("Error running whoami, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}
	if err := a.runCidrWhitelist(); err != nil {
		netcheckLogger.Error("Error running cidrwhitelist, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}
	if err := a.runDns(); err != nil {
		netcheckLogger.Error("Error running dns, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}
	if err := a.runWebhost(); err != nil {
		netcheckLogger.Error("Error running webhost, interrupting netcheck", map[string]string{"err": err.Error()})
		return
	}

	netcheckLogger.Debug("Net check: completed", make(map[string]string))
}

func NetCheck(configPath string) error {
	netcheckLogger.Debug("Loading config", make(map[string]string))
	if err := config.Load(configPath); err != nil {
		netcheckLogger.Error("Error loading config", map[string]string{"err": err.Error()})
	}

	appMu.Lock()
	defer appMu.Unlock()

	if app == nil {
		netcheckLogger.Debug("Creating new app", make(map[string]string))
		app = NewNetCheckApp()

		netcheckLogger.Debug("Running app", make(map[string]string))
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
		netcheckLogger.Debug("Cancel netcheck", make(map[string]string))
		app.interrupt <- true
		app.cancel()
		app = nil
	} else {
		netcheckLogger.Debug("No need to cancel netcheck", make(map[string]string))
	}
}
