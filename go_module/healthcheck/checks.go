package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"go_module/common"
	"go_module/log"
	"net"
	"net/http"
)

// Check errors
var (
	ErrConnectionCheck   = errors.New("connection check error")
	ErrClientHealthCheck = errors.New("client health check error")
)

func connectionCheck() error {
	log.Infof("[HC] Check: connection check")
	activeClients := common.Client.GetClientNames(true)

	if len(activeClients) == 0 {
		log.Infof("[HC] No vpn clients turned on")

		return ErrConnectionCheck
	}

	return nil
}

func activeClientsCheck() error {
	log.Infof("[HC] Check: clients health checks")
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
	log.Infof("[HC] Check: dns resolution check %s", host)

	log.Infof("[HC] With timeout = %v", dnsTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()

	_, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}

	return nil
}

func pingHostCheck(host string) error {
	log.Infof("[HC] Check: ping hosts %s", host)

	log.Infof("[HC] With timeout = %v", pingTimeout)

	log.Infof("[HC] Sending GET request to %s", host)
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", host, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed request init: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed request send: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Errorf("failed request body close: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("invalid status code: %d", resp.StatusCode)
	}

	return nil
}
