package netcheck

import (
	"context"
	"errors"
	"fmt"
	"go_module/log"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/checkers"
)

type NetCheckApp struct {
	ctx       context.Context
	cancel    context.CancelFunc
	interrupt chan bool
}

var InterruptedError error = errors.New("Check interrupted")
var NoInternetConnectionError error = errors.New("No internet connection")
var UserUnderWhitelistsError error = errors.New("User under whitelists")

func NewNetCheckApp() *NetCheckApp {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := make(chan bool, 1)

	return &NetCheckApp{ctx: ctx, cancel: cancel, interrupt: interrupt}
}

func (app *NetCheckApp) runWhoami() error {
	log.Infof("[NETCHECK] === WHOAMI ===")
	result, err := checkers.Whoami()

	if err != nil {
		return fmt.Errorf("Error running whoami check: %v", err)
	} else {
		log.Infof("[NETCHECK] IP      : %s", result.Ip)
		log.Infof("[NETCHECK] Subnet  : %s", result.Subnet)
		log.Infof("[NETCHECK] Asn     : %s", result.Asn)
		log.Infof("[NETCHECK] Org     : %s", result.Org)
		log.Infof("[NETCHECK] Location: %s", result.Location)

		return nil
	}
}

func (app *NetCheckApp) runCidrWhitelist() error {
	err := checkers.CidrWhitelist()

	log.Infof("[NETCHECK] === CIDRWHITELIST ===")
	if err != nil {
		switch err {
		case checkers.ErrCidrWhitelistDetected:
			log.Infof("[NETCHECK] You are under whitelist!")

			return UserUnderWhitelistsError
		case checkers.ErrCidrWhitelistNoInetAccess:
			log.Infof("[NETCHECK] You have no internet connection!")

			return NoInternetConnectionError
		default:
			return fmt.Errorf("Error running cidr whitelist check: %v", err)
		}
	} else {
		log.Infof("[NETCHECK] You are NOT under whitelist!")

		return nil
	}
}

func (app *NetCheckApp) runWebhost() error {
	runnerOpt := checkers.WebhostGochanRunnerOpt{Ctx: app.ctx, Mode: checkers.WebHostModePopular}
	out := checkers.WebhostGochanRunner(runnerOpt)

	log.Infof("[NETCHECK] === WEBHOST ===")
	for {
		select {
		case <-app.interrupt:
			log.Infof("[NETCHECK] WEBHOST check interrupted")

			return InterruptedError
		case msg1, ok := <-out.Out:
			if !ok {
				log.Infof("[NETCHECK] WEBHOST check completed")

				return nil
			}

			log.Infof("[NETCHECK] IpInfo : %s asn=%d %s \"%s\" %s", msg1.Out.IpInfo.Ip, msg1.Out.IpInfo.Asn, msg1.Out.IpInfo.Subnet, msg1.Out.IpInfo.Org, msg1.Out.IpInfo.CountryIso)
			log.Infof("[NETCHECK] Port   : %d", msg1.Out.Port)
			log.Infof("[NETCHECK] TlsV   : %d", msg1.Out.TlsV)
			log.Infof("[NETCHECK] Sni    : %s", msg1.Out.Sni)
			log.Infof("[NETCHECK] Host   : %s", msg1.Out.Host)

			if msg1.Out.Alive != nil {
				log.Infof("[NETCHECK] Alive  : %v", msg1.Out.Alive)
			} else {
				log.Infof("[NETCHECK] Alive  : OK")
			}

			if msg1.Out.Tcp1620 != nil {
				log.Infof("[NETCHECK] Tcp1620: %v", msg1.Out.Tcp1620)
			} else {
				log.Infof("[NETCHECK] Tcp1620: OK")
			}

			log.Infof("[NETCHECK] ===============")
		}
	}
}

type DnsCheckTable struct {
	provider    string
	plainResult string
	dohResult   string
}

func (app *NetCheckApp) runDns() error {
	leak := checkers.DnsLeakGochan(app.ctx)
	providerPlain := checkers.DnsPlainGochan(app.ctx)
	providerDoh := checkers.DnsDohGochan(app.ctx)
	result := make(map[string]DnsCheckTable)

	defer func() {
		log.Infof("[NETCHECK] Collected data")

		log.Infof("[NETCHECK] | %-15v | %-10v | %-10v |", "Provider", "Plain", "DoH")
		for _, row := range result {
			log.Infof("[NETCHECK] | %-15v | %-10v | %-10v |", row.provider, row.plainResult, row.dohResult)
		}
	}()

	for {
		select {
		case <-app.interrupt:
			log.Infof("[NETCHECK] WEBHOST check interrupted")

			return InterruptedError
		case v, ok := <-providerPlain:
			if !ok {
				providerPlain = nil

				if providerPlain == nil && leak == nil && providerDoh == nil {
					return nil
				} else {
					continue
				}
			}

			var verdict string
			if v.Verdict == nil {
				verdict = "OK"
			} else {
				verdict = v.Verdict.Error()
			}

			log.Infof("[NETCHECK] Plain: %v - %v", v.Provider, verdict)

			if _, ok := result[v.Provider]; !ok {
				result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: "N/A", dohResult: "N/A"}
			}

			value, _ := result[v.Provider]
			result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: verdict, dohResult: value.dohResult}
		case v, ok := <-providerDoh:
			if !ok {
				providerDoh = nil

				if providerPlain == nil && leak == nil && providerDoh == nil {
					return nil
				} else {
					continue
				}
			}

			var verdict string
			if v.Verdict == nil {
				verdict = "OK"
			} else {
				verdict = v.Verdict.Error()
			}

			log.Infof("[NETCHECK] Doh  : %v - %v", v.Provider, verdict)

			if _, ok := result[v.Provider]; !ok {
				result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: "N/A", dohResult: "N/A"}
			}

			value, _ := result[v.Provider]
			result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: value.plainResult, dohResult: verdict}
		case v, ok := <-leak:
			if !ok {
				leak = nil

				if providerPlain == nil && leak == nil && providerDoh == nil {
					return nil
				} else {
					continue
				}
			}
			log.Infof("[NETCHECK] Leak : %v - %v", v.Items, v.Err)
		}
	}
}
