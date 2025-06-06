package internal

import (
	"context"
	"fmt"
	"github.com/Jigsaw-Code/outline-sdk/dns"
	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/dnstruncate"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/config"
	"github.com/Jigsaw-Code/outline-sdk/x/connectivity"
	"log"
)

type outlinePacketProxy struct {
	network.DelegatePacketProxy

	remote, fallback network.PacketProxy
	remotePl         transport.PacketListener
}

func newOutlinePacketProxy(transportConfig string) (opp *outlinePacketProxy, err error) {
	opp = &outlinePacketProxy{}

	log.Println("outline client: creating UDP packet listener...")
	if opp.remotePl, err = config.NewPacketListener(transportConfig); err != nil {
		return nil, fmt.Errorf("failed to create UDP packet listener: %w", err)
	}

	log.Println("outline client: creating UDP packet proxy...")
	if opp.remote, err = network.NewPacketProxyFromPacketListener(opp.remotePl); err != nil {
		return nil, fmt.Errorf("failed to create UDP packet proxy: %w", err)
	}

	log.Println("outline client: creating DNS truncate packet proxy...")
	if opp.fallback, err = dnstruncate.NewPacketProxy(); err != nil {
		return nil, fmt.Errorf("failed to create DNS truncate packet proxy: %w", err)
	}

	log.Println("outline client: creating delegate UDP proxy...")
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
		log.Println(fmt.Sprintf("connectivity test failed. Refresh skipped. Error: %v\n", err))
		return err
	}
	if result != nil {
		log.Println("remote server cannot handle UDP traffic, switch to DNS truncate mode.")
		return proxy.SetProxy(proxy.fallback)
	} else {
		log.Println("remote server supports UDP, we will delegate all UDP packets to it")
		return proxy.SetProxy(proxy.remote)
	}
}
