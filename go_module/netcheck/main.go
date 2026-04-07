package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/config"
	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/updater"
)

var app *NetCheckApp = nil
var appMu sync.Mutex

func (app *NetCheckApp) netCheckInternal() {
	log.Printf("[NETCHECK] Net check: start\n")

	app.runWhoami()
	app.runCidrWhitelist()
	app.runWebhost()

	log.Printf("[NETCHECK] Net check: completed\n")

	appMu.Lock()
	defer appMu.Unlock()
	app = nil
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

	appMu.Lock()
	defer appMu.Unlock()

	if app == nil {
		app = NewNetCheckApp()

		go app.netCheckInternal()

		return nil
	} else {
		return fmt.Errorf("App is running")
	}
}

func CancelNetCheck() {
	log.Printf("[NETCHECK] Cancel netcheck\n")
	app.interrupt <- true
	app.cancel()

	appMu.Lock()
	defer appMu.Unlock()
	app = nil
}

func main() {
	cfgPath := flag.String("cfg", "dpi-checkers/ru/dpi-ch/config/default.yaml", ".yaml config path")
	flag.Parse()

	var err error

	if err = NetCheck(*cfgPath); err != nil {
		log.Printf("Error running netcheck: %v", err)
	}

	time.Sleep(time.Second * 2)

	if err = NetCheck(*cfgPath); err != nil {
		log.Printf("Error running netcheck: %v", err)
	}

	time.Sleep(time.Second * 2)

	CancelNetCheck()

	time.Sleep(time.Second * 2)
}
