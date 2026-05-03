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

var (
	InterruptedError          error = errors.New("Check interrupted")
	NoInternetConnectionError error = errors.New("No internet connection")
	UserUnderWhitelistsError  error = errors.New("User under whitelists")
)

func NewNetCheckApp() *NetCheckApp {
	ctx, cancel := context.WithCancel(context.Background())
	interrupt := make(chan bool, 1)

	return &NetCheckApp{ctx: ctx, cancel: cancel, interrupt: interrupt}
}

func (app *NetCheckApp) runWhoami() error {
	log.Debugf(Category, "Running WHOAMI check")
	result, err := checkers.Whoami()

	if err != nil {
		return fmt.Errorf("failed running whoami check: %v", err)
	} else {
		app.whoami = result
		return nil
	}
}

func (app *NetCheckApp) runCidrWhitelist() error {
	log.Debugf(Category, "Running CIDRWHITELIST check")

	err := checkers.CidrWhitelist()

	if err != nil {
		switch err {
		case checkers.ErrCidrWhitelistDetected:
			log.Debugf(Category, "User under whitelist")

			return UserUnderWhitelistsError
		case checkers.ErrCidrWhitelistNoInetAccess:
			log.Debugf(Category, "User have no internet connection")

			return NoInternetConnectionError
		default:
			log.Debugf(Category, "Undefined error ocurred")

			return fmt.Errorf("failed running cidr whitelist check: %v", err)
		}
	} else {
		log.Debugf(Category, "User NOT under any whitelist")

		return nil
	}
}

func (app *NetCheckApp) runWebhost() error {
	log.Debug(Category, "Running WEBHOST check", make(map[string]any))

	runnerOpt := checkers.WebhostGochanRunnerOpt{Ctx: app.ctx, Mode: checkers.WebHostModePopular}
	out := checkers.WebhostGochanRunner(runnerOpt)

	for {
		select {
		case <-app.interrupt:
			log.Debugf(Category, "Webhost check: interrupting")

			return InterruptedError
		case msg1, ok := <-out.Out:
			if !ok {
				log.Debugf(Category, "Webhost check: completed")

				return nil
			}

			if msg1.Out.Alive == nil && msg1.Out.Tcp1620 == nil {
				log.Info(Category, "Webhost check: success", map[string]any{
					"Host_Ip":         msg1.Out.IpInfo.Ip,
					"Host_Asn":        msg1.Out.IpInfo.Asn,
					"Host_Subnet":     msg1.Out.IpInfo.Subnet,
					"Host_Org":        msg1.Out.IpInfo.Org,
					"Host_CountryIso": msg1.Out.IpInfo.CountryIso,
					"Host_Port":       msg1.Out.Port,
					"Host_TlsV":       msg1.Out.TlsV,
					"Host_Sni":        msg1.Out.Sni,
					"Host_Host":       msg1.Out.Host,
					"Result_Alive":    nil,
					"Result_Tcp1620":  nil,
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			} else {
				log.Error(Category, "Webhost check: failed", map[string]any{
					"Host_Ip":         msg1.Out.IpInfo.Ip,
					"Host_Asn":        msg1.Out.IpInfo.Asn,
					"Host_Subnet":     msg1.Out.IpInfo.Subnet,
					"Host_Org":        msg1.Out.IpInfo.Org,
					"Host_CountryIso": msg1.Out.IpInfo.CountryIso,
					"Host_Port":       msg1.Out.Port,
					"Host_TlsV":       msg1.Out.TlsV,
					"Host_Sni":        msg1.Out.Sni,
					"Host_Host":       msg1.Out.Host,
					"Result_Alive":    msg1.Out.Alive,
					"Result_Tcp1620":  msg1.Out.Tcp1620,
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			}
		}
	}
}

type DnsCheckTable struct {
	provider    string
	plainResult error
	dohResult   error
}

func (app *NetCheckApp) runDns() error {
	log.Debug(Category, "Running DNS check", make(map[string]any))

	leak := checkers.DnsLeakGochan(app.ctx)
	providerPlain := checkers.DnsPlainGochan(app.ctx)
	providerDoh := checkers.DnsDohGochan(app.ctx)
	result := make(map[string]DnsCheckTable)

	defer func() {
		log.Debug(Category, "Collected data", make(map[string]any))

		args := make(map[string]any, len(result)*3+5)

		for index, row := range result {
			args[fmt.Sprintf("Result_%s_provider", index)] = row.provider
			args[fmt.Sprintf("Result_%v_plain", index)] = row.plainResult
			args[fmt.Sprintf("Result_%v_doh", index)] = row.dohResult
		}

		args["Whoami_Ip"] = app.whoami.Ip
		args["Whoami_Subnet"] = app.whoami.Subnet
		args["Whoami_Asn"] = app.whoami.Asn
		args["Whoami_Org"] = app.whoami.Org
		args["Whoami_Location"] = app.whoami.Location

		log.Debug(Category, "Dns result", args)
	}()

	var naError = errors.New("N/A")
	for {
		select {
		case <-app.interrupt:
			log.Debug(Category, "DNS check: interrupted", make(map[string]any))

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

			if v.Verdict == nil {
				log.Debug(Category, "DNS check: plain step success", map[string]any{
					"Provider":        v.Provider,
					"Verdict":         v.Verdict,
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			} else {
				log.Error(Category, "DNS check: plain step failed", map[string]any{
					"Provider":        v.Provider,
					"Verdict":         v.Verdict,
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			}

			if _, ok := result[v.Provider]; !ok {
				result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: naError, dohResult: naError}
			}

			value, _ := result[v.Provider]
			result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: v.Verdict, dohResult: value.dohResult}
		case v, ok := <-providerDoh:
			if !ok {
				providerDoh = nil

				if providerPlain == nil && leak == nil && providerDoh == nil {
					return nil
				} else {
					continue
				}
			}

			if v.Verdict == nil {
				log.Debug(Category, "DNS check: doh step success", map[string]any{
					"Provider":        v.Provider,
					"Verdict":         v.Verdict,
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			} else {
				log.Error(Category, "DNS check: doh step failed", map[string]any{
					"Provider":        v.Provider,
					"Verdict":         v.Verdict,
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			}

			if _, ok := result[v.Provider]; !ok {
				result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: naError, dohResult: naError}
			}

			value, _ := result[v.Provider]
			result[v.Provider] = DnsCheckTable{provider: v.Provider, plainResult: value.plainResult, dohResult: v.Verdict}
		case v, ok := <-leak:
			if !ok {
				leak = nil

				if providerPlain == nil && leak == nil && providerDoh == nil {
					return nil
				} else {
					continue
				}
			}

			if v.Err == nil {
				log.Debug(Category, "DNS check: leak step success", map[string]any{
					"Items_count":     strconv.Itoa(len(v.Items)),
					"Err":             v.Err.Error(),
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			} else {
				log.Error(Category, "DNS check: leak step failed", map[string]any{
					"Items_count":     strconv.Itoa(len(v.Items)),
					"Err":             v.Err.Error(),
					"Whoami_Ip":       app.whoami.Ip,
					"Whoami_Subnet":   app.whoami.Subnet,
					"Whoami_Asn":      app.whoami.Asn,
					"Whoami_Org":      app.whoami.Org,
					"Whoami_Location": app.whoami.Location,
				})
			}
		}
	}
}
