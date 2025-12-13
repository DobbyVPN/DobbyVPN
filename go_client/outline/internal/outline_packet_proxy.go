package internal

import (
	"context"
	"fmt"

	"github.com/Jigsaw-Code/outline-sdk/dns"
	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/dnstruncate"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/connectivity"
	log "go_client/logger"
)

type outlinePacketProxy struct {
	network.DelegatePacketProxy

	remote, fallback network.PacketProxy
	remotePl         transport.PacketListener
}

func newOutlinePacketProxy(transportConfig string) (opp *outlinePacketProxy, err error) {
	opp = &outlinePacketProxy{}

	log.Infof("outline client: creating DNS truncate packet proxy (fallback)...")
	if opp.fallback, err = dnstruncate.NewPacketProxy(); err != nil {
		return nil, fmt.Errorf("failed to create DNS truncate packet proxy: %w", err)
	}

	log.Infof("outline client: creating UDP packet listener...")
	if opp.remotePl, err = configProviders.NewPacketListener(context.Background(), transportConfig); err != nil {
		// UDP not supported (e.g., WebSocket without udp_path), use DNS truncate mode
		log.Infof("UDP packet listener not available (%v), using DNS truncate mode", err)
		if opp.DelegatePacketProxy, err = network.NewDelegatePacketProxy(opp.fallback); err != nil {
			return nil, fmt.Errorf("failed to create delegate UDP proxy: %w", err)
		}
		return opp, nil
	}

	log.Infof("outline client: creating UDP packet proxy...")
	if opp.remote, err = network.NewPacketProxyFromPacketListener(opp.remotePl); err != nil {
		return nil, fmt.Errorf("failed to create UDP packet proxy: %w", err)
	}

	log.Infof("outline client: creating delegate UDP proxy...")
	if opp.DelegatePacketProxy, err = network.NewDelegatePacketProxy(opp.fallback); err != nil {
		return nil, fmt.Errorf("failed to create delegate UDP proxy: %w", err)
	}

	return
}

func (proxy *outlinePacketProxy) testConnectivityAndRefresh(resolverAddr, domain string) error {
	// If UDP is not available, we're already in DNS truncate mode
	if proxy.remotePl == nil {
		log.Infof("UDP not available, staying in DNS truncate mode")
		return nil
	}

	dialer := transport.PacketListenerDialer{Listener: proxy.remotePl}
	dnsResolver := dns.NewUDPResolver(dialer, resolverAddr)
	result, err := connectivity.TestConnectivityWithResolver(context.Background(), dnsResolver, domain)
	if err != nil {
		log.Infof(fmt.Sprintf("connectivity test failed. Refresh skipped. Error: %v\n", err))
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
