package netcheck

import (
	"context"
	"fmt"
	"go_module/log"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/config"
)

const HASH_POSTFIX = ".hash"

var app *NetCheckApp = nil
var appMu sync.Mutex

func (a *NetCheckApp) netCheckInternal() {
	log.Infof("[NETCHECK] Net check: start")

	var err error

	if err = a.runWhoami(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running whoami, interrupting netcheck: %v", err)
		return
	}
	if err = a.runCidrWhitelist(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running cidrwhitelist, interrupting netcheck: %v", err)
		return
	}
	if err = a.runWebhost(); err != nil {
		log.Infof("[NETCHECK] [ERROR] Error running webhost, interrupting netcheck: %v", err)
		return
	}

	log.Infof("[NETCHECK] Net check: completed")

	appMu.Lock()
	app = nil
	appMu.Unlock()
}

func GeoliteUpdate(ctx context.Context) error {
	cfg := config.Get().Updater
	dir := path.Join(cfg.RootDir, cfg.Geolite.Dir)
	log.Infof("[updater/geolite] starting update: %v", dir)

	if err := geolitePartUpdate(ctx, cfg.Geolite.CidrAs.From, path.Join(dir, cfg.Geolite.CidrAs.To)); err != nil {
		return err
	}
	if err := geolitePartUpdate(ctx, cfg.Geolite.CidrCountry.From, path.Join(dir, cfg.Geolite.CidrCountry.To)); err != nil {
		return err
	}
	if err := geolitePartUpdate(ctx, cfg.Geolite.GeonameidCountry.From, path.Join(dir, cfg.Geolite.GeonameidCountry.To)); err != nil {
		return err
	}
	log.Infof("[updater/geolite] successfully updated")

	return nil
}

func geolitePartUpdate(ctx context.Context, from, to string) error {
	log.Infof("[updater/geolite] part update %s - %s", from, to)

	cfg := config.Get().Updater

	log.Infof("[updater/geolite] geoliteCidrAsUpdate:download")
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	download(ctx, contentUrl(
		cfg.Geolite.Owner,
		cfg.Geolite.Repo,
		from,
		cfg.Geolite.Branch,
	), to)

	return nil
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}

	return nil
}

func contentUrl(owner, repo, path, branch string) string {
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/refs/heads/%s/%s",
		url.PathEscape(owner),
		url.PathEscape(repo),
		url.PathEscape(branch),
		url.PathEscape(path),
	)
}

func NetCheck(configPath string) error {
	log.Infof("[NETCHECK] Loading config")
	if err := config.Load(configPath); err != nil {
		return fmt.Errorf("Error loading config: %v", err)
	}

	log.Infof("[NETCHECK] Geolite update")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	if err := GeoliteUpdate(ctx); err != nil {
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
		app.interrupt <- true
		app.cancel()
		app = nil
	} else {
		log.Infof("[NETCHECK] No need to cancel netcheck")
	}
}
