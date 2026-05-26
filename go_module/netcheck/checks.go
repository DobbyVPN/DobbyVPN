package netcheck

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"go_module/log"

	"github.com/hyperion-cs/dpi-checkers/ru/dpi-ch/checkers"
)

type NetCheckApp struct {
	whoami    checkers.WhoamiResult
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
	log.Debugf(Category, "Running WHOAMI check", make(map[string]string))
	result, err := checkers.Whoami()

	if err != nil {
		return fmt.Errorf("Error running whoami check: %v", err)
	} else {
		app.whoami = result
		return nil
	}
}

func (app *NetCheckApp) runCidrWhitelist() error {
	log.Debugf(Category, "Running CIDRWHITELIST check", make(map[string]string))

	err := checkers.CidrWhitelist()

	if err != nil {
		switch err {
		case checkers.ErrCidrWhitelistDetected:
			log.Debugf(Category, "User under whitelist", make(map[string]string))

			return UserUnderWhitelistsError
		case checkers.ErrCidrWhitelistNoInetAccess:
			log.Debugf(Category, "User have no internet connection", make(map[string]string))

			return NoInternetConnectionError
		default:
			log.Debugf(Category, "Undefined error ocurred", make(map[string]string))

			return fmt.Errorf("Error running cidr whitelist check: %v", err)
		}
	} else {
		log.Debugf(Category, "User NOT under any whitelist", make(map[string]string))

		return nil
	}
}

func (app *NetCheckApp) runWebhost() error {
	log.Debugf(Category, "Running WEBHOST check", make(map[string]string))

	runnerOpt := checkers.WebhostGochanRunnerOpt{Ctx: app.ctx, Mode: checkers.WebHostModePopular}
	out := checkers.WebhostGochanRunner(runnerOpt)

	for {
		select {
		case <-app.interrupt:
			log.Debugf(Category, "Interrupting", make(map[string]string))

			return InterruptedError
		case msg1, ok := <-out.Out:
			if !ok {
				log.Debugf(Category, "Completed", make(map[string]string))

				return nil
			}

			log.Debugf(Category, "Webhost check results:", map[string]string{
				"Whoami_Ip":       app.whoami.Ip,
				"Whoami_Subnet":   app.whoami.Subnet,
				"Whoami_Asn":      app.whoami.Asn,
				"Whoami_Org":      app.whoami.Org,
				"Whoami_Location": app.whoami.Location,
				"Host_Ip":         fmt.Sprintf("%v", msg1.Out.IpInfo.Ip),
				"Host_Asn":        fmt.Sprintf("%v", msg1.Out.IpInfo.Asn),
				"Host_Subnet":     fmt.Sprintf("%v", msg1.Out.IpInfo.Subnet),
				"Host_Org":        fmt.Sprintf("%v", msg1.Out.IpInfo.Org),
				"Host_CountryIso": fmt.Sprintf("%v", msg1.Out.IpInfo.CountryIso),
				"Host_Port":       fmt.Sprintf("%v", msg1.Out.Port),
				"Host_TlsV":       fmt.Sprintf("%v", msg1.Out.TlsV),
				"Host_Sni":        fmt.Sprintf("%v", msg1.Out.Sni),
				"Host_Host":       fmt.Sprintf("%v", msg1.Out.Host),
				"Result_Alive":    fmt.Sprintf("%v", msg1.Out.Alive),
				"Result_Tcp1620":  fmt.Sprintf("%v", msg1.Out.Tcp1620),
			})
		}
	}
}

type DnsCheckTable struct {
	provider    string
	plainResult string
	dohResult   string
}

func (app *NetCheckApp) runDns() error {
	log.Debugf(Category, "Running DNS check", make(map[string]string))

	leak := checkers.DnsLeakGochan(app.ctx)
	providerPlain := checkers.DnsPlainGochan(app.ctx)
	providerDoh := checkers.DnsDohGochan(app.ctx)
	result := make(map[string]DnsCheckTable)

	defer func() {
		log.Debugf(Category, "Collected data", make(map[string]string))

		args := make(map[string]string, len(result)*3+5)

		for index, row := range result {
			args[fmt.Sprintf("Result_%s_provider", index)] = row.provider
			args[fmt.Sprintf("Result_%s_plain", index)] = row.plainResult
			args[fmt.Sprintf("Result_%s_doh", index)] = row.dohResult
		}

		args["Whoami_Ip"] = app.whoami.Ip
		args["Whoami_Subnet"] = app.whoami.Subnet
		args["Whoami_Asn"] = app.whoami.Asn
		args["Whoami_Org"] = app.whoami.Org
		args["Whoami_Location"] = app.whoami.Location

		log.Debugf(Category, "Dns result", args)
	}()

	for {
		select {
		case <-app.interrupt:
			log.Debugf(Category, "Check interrupted", make(map[string]string))

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

			log.Debugf(Category, "Plain step", map[string]string{"Provider": v.Provider, "verdict": verdict})

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

			log.Debugf(Category, "Doh step", map[string]string{"Provider": v.Provider, "verdict": verdict})

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

			log.Debugf(Category, "Leak step", map[string]string{"Items_count": strconv.Itoa(len(v.Items)), "Err": v.Err.Error()})
		}
	}
}
