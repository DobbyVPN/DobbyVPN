package main

import (
	"flag"
	"go_module/desktop_exports/api"
	"go_module/log"
	"os"
	"os/signal"
	"syscall"

	"github.com/BurntSushi/toml"
)

type CloakConfig struct {
	LocalHost string
	LocalPort string
	Config    string
	Udp       bool
}

type OutlineConfig struct {
	Key   string
	Cloak *CloakConfig
}

type TomlConfigs struct {
	Outline *OutlineConfig
}

func main() {
	var (
		configPath string
	)

	flag.StringVar(&configPath, "config", "config.toml", "VPN client config")

	flag.Parse()

	log.SetStdOut()

	logger := log.GetLogger().NewSubLogger("CLI")

	logger.Debug("Reading config ", map[string]string{configPath: configPath})

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.Error("Error reading file: %v", map[string]string{"err": err.Error()})
		os.Exit(1)
	}

	var cfg TomlConfigs
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		logger.Error("Error decoding file: %v", map[string]string{"err": err.Error()})
		os.Exit(1)
	}

	if cfg.Outline != nil {
		if cfg.Outline.Cloak != nil {
			logger.Debug("Cloak config detected", map[string]string{})

			api.StartCloakClient(*&cfg.Outline.Cloak.LocalHost, *&cfg.Outline.Cloak.LocalPort, *&cfg.Outline.Cloak.Config, *&cfg.Outline.Cloak.Udp)
		}

		logger.Debug("Outline config detected", map[string]string{})
		result := api.StartOutline(cfg.Outline.Key)

		if result != 0 {
			logger.Error("Failed start outilne: %v", map[string]string{})
			api.StopOutline()
			if cfg.Outline.Cloak != nil {
				api.StopCloakClient()
			}

			os.Exit(1)
		}

		sigCh := make(chan os.Signal, 1)

		signal.Notify(sigCh, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

		logger.Info("Running... press Ctrl+C to stop", map[string]string{})

		<-sigCh
		logger.Info("Ctrl‑C received, exiting gracefully", map[string]string{})

		api.StopOutline()
		if cfg.Outline.Cloak != nil {
			api.StopCloakClient()
		}

		return
	}

	logger.Info("No VPN detected, exit", map[string]string{})
}
