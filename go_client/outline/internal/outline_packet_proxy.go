package internal

import (
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"

	"github.com/Jigsaw-Code/outline-sdk/dns"
	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/dnstruncate"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
	"github.com/Jigsaw-Code/outline-sdk/x/connectivity"
)

type outlinePacketProxy struct {
	network.DelegatePacketProxy

	remote, fallback network.PacketProxy
	remotePl         transport.PacketListener
}

func newOutlinePacketProxy(transportConfig string) (opp *outlinePacketProxy, err error) {
	opp = &outlinePacketProxy{}

	if opp.remotePl, err = configurl.NewPacketListener(transportConfig); err != nil {
		return nil, fmt.Errorf("failed to create UDP packet listener: %w", err)
	}
	if opp.remote, err = network.NewPacketProxyFromPacketListener(opp.remotePl); err != nil {
		return nil, fmt.Errorf("failed to create UDP packet proxy: %w", err)
	}
	if opp.fallback, err = dnstruncate.NewPacketProxy(); err != nil {
		return nil, fmt.Errorf("failed to create DNS truncate packet proxy: %w", err)
	}
	if opp.DelegatePacketProxy, err = network.NewDelegatePacketProxy(opp.fallback); err != nil {
		return nil, fmt.Errorf("failed to create delegate UDP proxy: %w", err)
	}

	return
}

func (proxy *outlinePacketProxy) testConnectivityAndRefresh(resolverAddr, domain string) error {
	dialer := transport.PacketListenerDialer{Listener: proxy.remotePl}
	dnsResolver := dns.NewUDPResolver(dialer, resolverAddr)
	result, err := connectivity.TestConnectivityWithResolver(context.Background(), dnsResolver, domain)
	if err != nil {
		log.Infof("connectivity test failed. Refresh skipped. Error: %v\n", err)
		return err
	}
	if result != nil {
		log.Infof("remote server cannot handle UDP traffic, switch to DNS truncate mode.")
		return proxy.SetProxy(proxy.fallback)
	} else {
		log.Infof("remote server supports UDP, we will delegate all UDP packets to it")
		return proxy.SetProxy(proxy.remote)
	}
}
