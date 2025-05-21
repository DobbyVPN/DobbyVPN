package internal

import (
	"context"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"net"
	"net/url"
	"strings"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/lwip2transport"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
)

const (
	connectivityTestDomain   = "www.google.com"
	connectivityTestResolver = "1.1.1.1:53"
)

type OutlineDevice struct {
	network.IPDevice
	sd    transport.StreamDialer
	pp    *outlinePacketProxy
	svrIP net.IP
}

var configModule = configurl.NewDefaultProviders()

func NewOutlineDevice(transportConfig string) (od *OutlineDevice, err error) {
	log.Infof("oultine client: resolving server IP from config...")
	ip, err := resolveShadowsocksServerIPFromConfig(transportConfig)
	if err != nil {
		return nil, err
	}
	od = &OutlineDevice{
		svrIP: ip,
	}

	log.Infof("outline client: creating stream dialer...")
	if od.sd, err = configModule.NewStreamDialer(context.TODO(), transportConfig); err != nil {
		return nil, fmt.Errorf("failed to create TCP dialer: %w", err)
	}

	log.Infof("outline client: creating packet proxy...")
	if od.pp, err = newOutlinePacketProxy(transportConfig); err != nil {
		return nil, fmt.Errorf("failed to create delegate UDP proxy: %w", err)
	}

	log.Infof("outline client: configuring lwIP...")
	if od.IPDevice, err = lwip2transport.ConfigureDevice(od.sd, od.pp); err != nil {
		return nil, fmt.Errorf("failed to configure lwIP: %w", err)
	}

	return
}

func (d *OutlineDevice) Close() error {
	return d.IPDevice.Close()
}

func (d *OutlineDevice) Refresh() error {
	return d.pp.testConnectivityAndRefresh(connectivityTestResolver, connectivityTestDomain)
}

func (d *OutlineDevice) GetServerIP() net.IP {
	return d.svrIP
}

func resolveShadowsocksServerIPFromConfig(transportConfig string) (net.IP, error) {
	if strings.Contains(transportConfig, "|") {
		return nil, errors.New("multi-part config is not supported")
	}
	if transportConfig = strings.TrimSpace(transportConfig); transportConfig == "" {
		return nil, errors.New("config is required")
	}
	url, err := url.Parse(transportConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if url.Scheme != "ss" {
		return nil, errors.New("config must start with 'ss://'")
	}
	ipList, err := net.LookupIP(url.Hostname())
	if err != nil {
		return nil, fmt.Errorf("invalid server hostname: %w", err)
	}

	// todo: we only tested IPv4 routing table, need to test IPv6 in the future
	for _, ip := range ipList {
		if ip = ip.To4(); ip != nil {
			return ip, nil
		}
	}
	return nil, errors.New("IPv6 only Shadowsocks server is not supported yet")
}
