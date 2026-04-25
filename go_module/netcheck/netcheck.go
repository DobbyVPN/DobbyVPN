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

	log.Debugf(Category, "Geolite update")
	ctx, cancel := context.WithCancel(a.ctx)
	if err := updater.GeoliteUpdate(ctx); err != nil {
		cancel()
		log.Error(Category, "Error run GeoliteUpdate", map[string]any{"err": err.Error()})
		return
	}

	log.Debugf(Category, "Net check: start")

	if err := a.runWhoami(); err != nil {
		log.Error(Category, "Whoami check: failed", map[string]any{"err": err.Error()})
		return
	} else {
		log.Info(Category, "Whoami check: success", make(map[string]any))
	}

	if err := a.runCidrWhitelist(); err != nil {
		if err == InterruptedError {
			log.Warnf(Category, "CidrWhitelist check: interrupted")
		} else {
			log.Error(Category, "CidrWhitelist check: failed", map[string]any{
				"err":             err,
				"Whoami_Ip":       app.whoami.Ip,
				"Whoami_Subnet":   app.whoami.Subnet,
				"Whoami_Asn":      app.whoami.Asn,
				"Whoami_Org":      app.whoami.Org,
				"Whoami_Location": app.whoami.Location,
			})
		}
		return
	} else {
		log.Info(Category, "CidrWhitelist check: success", map[string]any{
			"Whoami_Ip":       app.whoami.Ip,
			"Whoami_Subnet":   app.whoami.Subnet,
			"Whoami_Asn":      app.whoami.Asn,
			"Whoami_Org":      app.whoami.Org,
			"Whoami_Location": app.whoami.Location,
		})
	}

	if err := a.runDns(); err != nil {
		if err == InterruptedError {
			log.Warnf(Category, "DNS check: interrupted")
		} else {
			log.Error(Category, "DNS check: failed", map[string]any{
				"err":             err,
				"Whoami_Ip":       app.whoami.Ip,
				"Whoami_Subnet":   app.whoami.Subnet,
				"Whoami_Asn":      app.whoami.Asn,
				"Whoami_Org":      app.whoami.Org,
				"Whoami_Location": app.whoami.Location,
			})
		}
		return
	} else {
		log.Info(Category, "DNS check: success", map[string]any{
			"Whoami_Ip":       app.whoami.Ip,
			"Whoami_Subnet":   app.whoami.Subnet,
			"Whoami_Asn":      app.whoami.Asn,
			"Whoami_Org":      app.whoami.Org,
			"Whoami_Location": app.whoami.Location,
		})
	}

	if err := a.runWebhost(); err != nil {
		if err == InterruptedError {
			log.Warnf(Category, "Webhosts check: interrupted")
		} else {
			log.Error(Category, "Webhosts check: failed", map[string]any{
				"err":             err,
				"Whoami_Ip":       app.whoami.Ip,
				"Whoami_Subnet":   app.whoami.Subnet,
				"Whoami_Asn":      app.whoami.Asn,
				"Whoami_Org":      app.whoami.Org,
				"Whoami_Location": app.whoami.Location,
			})
		}
		return
	} else {
		log.Info(Category, "Webhosts check: success", map[string]any{
			"Whoami_Ip":       app.whoami.Ip,
			"Whoami_Subnet":   app.whoami.Subnet,
			"Whoami_Asn":      app.whoami.Asn,
			"Whoami_Org":      app.whoami.Org,
			"Whoami_Location": app.whoami.Location,
		})
	}

	log.Infof(Category, "Net check: completed")
}

func NetCheck(configPath string) error {
	log.Debugf(Category, "Loading config")
	if err := config.Load(configPath); err != nil {
		log.Debugf(Category, "Error loading config: %v", err)
	}

	appMu.Lock()
	defer appMu.Unlock()

	if app == nil {
		log.Debugf(Category, "Creating new app")
		app = NewNetCheckApp()

		log.Debugf(Category, "Running app")
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
		log.Debugf(Category, "Cancel netcheck")
		app.interrupt <- true
		app.cancel()
		app = nil
	} else {
		log.Debugf(Category, "No need to cancel netcheck")
	}
}
