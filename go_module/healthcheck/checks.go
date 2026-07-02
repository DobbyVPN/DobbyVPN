package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"go_module/common"
	hcCommon "go_module/healthcheck/common"
	"go_module/log"
	"net"
	"net/http"
	"runtime"
	"strings"
)

// Check errors
var (
	ErrConnectionCheck   = errors.New("connection check error")
	ErrClientHealthCheck = errors.New("client health check error")
)

const (
	goosAndroid = "android"
	goosIOS     = "ios"
)

func connectionCheck() error {
	log.Debugf(hcCommon.Category, "Check: connection check")
	if runtime.GOOS == goosIOS {
		log.Debugf(hcCommon.Category, "Skipping active Go client check on iOS app process")
		return nil
	}

	activeClients := common.Client.GetClientNames(true)

	if len(activeClients) == 0 {
		log.Debugf(hcCommon.Category, "No vpn clients turned on")

		return ErrConnectionCheck
	}

	return nil
}

func activeClientsCheck() error {
	log.Debugf(hcCommon.Category, "Check: clients health checks")
	if runtime.GOOS == goosIOS {
		log.Debugf(hcCommon.Category, "Skipping Go client health checks on iOS app process")
		return nil
	}

	activeClients := common.Client.GetClientNames(true)

	for _, clientName := range activeClients {
		err := common.Client.HealthCheck(clientName)
		if err != nil {
			return ErrClientHealthCheck
		}
	}

	return nil
}

func dnsResolveCheck(host string) error {
	if runtime.GOOS == goosAndroid || runtime.GOOS == goosIOS {
		log.Debugf(hcCommon.Category, "Skipping standalone DNS resolution check %s on %s", host, runtime.GOOS)
		return nil
	}

	log.Debugf(hcCommon.Category, "Check: dns resolution check %s with timeout = %v", host, dnsTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()

	_, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}

	return nil
}

func pingHostCheck(host string) error {
	log.Debugf(hcCommon.Category, "Check: ping host %s with timeout = %v", host, pingTimeout)

	log.Debugf(hcCommon.Category, "Sending GET request to %s", host)
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", host, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed request init: %w", err)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed request send: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("failed request body close: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		log.Warnf(hcCommon.Category, "invalid status code: %d", resp.StatusCode)
	}

	return nil
}

func anyHTTPPingCheck(hosts []string) error {
	log.Debugf(hcCommon.Category, "Check: HTTP connectivity candidates=%s", strings.Join(hosts, ", "))

	var errs []error
	successes := 0
	for _, host := range hosts {
		if err := pingHostCheck(host); err != nil {
			log.Warnf(hcCommon.Category, "HTTP connectivity candidate failed host=%s error=%v", host, err)
			errs = append(errs, fmt.Errorf("%s: %w", host, err))
			continue
		}

		successes++
		log.Debugf(hcCommon.Category, "HTTP connectivity candidate succeeded host=%s", host)
	}

	if successes != len(hosts) {
		return fmt.Errorf("HTTP connectivity candidates failed passed=%d total=%d: %w", successes, len(hosts), errors.Join(errs...))
	}

	return nil
}
