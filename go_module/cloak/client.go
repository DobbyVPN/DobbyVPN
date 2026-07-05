package cloak

import (
	"context"
	"encoding/json"
	"fmt"
	"go_module/common"
	"go_module/dnscache"
	"net"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cbeuw/Cloak/exported_client"

	"go_module/log"
)

const Name = "cloak"

const remoteHostResolveTimeout = 750 * time.Millisecond

var (
	client      *exported_client.CkClient
	mu          sync.Mutex
	cloakConfig exported_client.Config
)

func StartCloakClient(localHost, localPort, config string, udp bool) (err error) {
	log.Debugf(Category, "StartCloakClient inner")

	// Handle panic
	defer func() {
		if r := recover(); r != nil {
			log.Debugf(Category, "StartCloakClient: recovered from panic: %v", r)
			err = fmt.Errorf("StartCloakClient panic: %v", r)
		}
	}()

	mu.Lock()
	defer mu.Unlock()

	log.Debugf(Category, "Get lock")

	if client != nil {
		log.Debugf(Category, "Need to stop old cloak client")
		mu.Unlock()
		StopCloakClient()
		mu.Lock()
	}

	log.Debugf(Category, "deleted old cloak client")

	var rawConfig exported_client.Config
	err = json.Unmarshal([]byte(config), &rawConfig)
	if err != nil {
		log.Debugf(Category, "cloak client: Failed to unmarshal config - %v", err)
		return fmt.Errorf("cloak client: failed to unmarshal config: %w", err)
	}
	log.Debugf(Category, "cloak client: rawConfig parsed successfully")

	rawConfig.RemoteHost, err = resolveRemoteHostIfNeeded(rawConfig.RemoteHost)
	if err != nil {
		log.Debugf(Category, "Can't resolve Remote Host: %v", err)
		return fmt.Errorf("cloak client: can't resolve remote host: %w", err)
	}
	rawConfig.LocalHost = localHost
	rawConfig.LocalPort = localPort
	rawConfig.UDP = udp
	log.Debugf(Category, "cloak client: rawConfig updated with LocalHost=%s, LocalPort=%s, UDP=%v", localHost, localPort, udp)

	// Forbidden words in logs
	log.AddForbiddenWord(string(rawConfig.UID))
	log.AddForbiddenWord(rawConfig.ServerName)
	log.AddForbiddenWord(rawConfig.RemoteHost)
	log.AddForbiddenWord(rawConfig.CDNWsUrlPath)
	log.AddForbiddenWord(rawConfig.CDNOriginHost)

	// Cloak routing
	cloakConfig = rawConfig
	common.Client.MarkInCriticalSection(Name)
	err = StartRoutingCloak(cloakConfig.RemoteHost)
	common.Client.MarkOutOffCriticalSection(Name)
	if err != nil {
		log.Debugf(Category, "Can't routing cloak, %v", err)
		return fmt.Errorf("cloak client: can't route cloak: %w", err)
	}
	log.Debugf(Category, "cloak client: Routed")

	client = exported_client.NewCkClient(rawConfig)

	common.Client.SetVpnClient(Name, client)
	err = common.Client.Connect(Name)
	if err != nil {
		log.Debugf(Category, "cloak client: Failed to connect to cloak client - %v", err)
		client = nil
		if cloakConfig.RemoteHost != "" {
			common.Client.MarkInCriticalSection(Name)
			StopRoutingCloak(cloakConfig.RemoteHost)
			common.Client.MarkOutOffCriticalSection(Name)
			cloakConfig.RemoteHost = ""
		}
		common.Client.MarkInactive(Name)
		return fmt.Errorf("cloak client: failed to connect: %w", err)
	}

	log.Debugf(Category, "cloak client connected")

	common.Client.MarkActive(Name)
	return nil
}

func StopCloakClient() {
	log.Debugf(Category, "StopCloakClient inner")

	// Handle panic
	defer func() {
		if r := recover(); r != nil {
			log.Debugf(Category, "StopCloakClient: recovered from panic: %v", r)
		}
	}()

	defer common.Client.MarkInactive(Name)
	mu.Lock()
	defer mu.Unlock()

	// Remove forbidden words in logs
	log.RemoveForbiddenWord(string(cloakConfig.UID))
	log.RemoveForbiddenWord(cloakConfig.ServerName)
	log.RemoveForbiddenWord(cloakConfig.RemoteHost)
	log.RemoveForbiddenWord(cloakConfig.CDNWsUrlPath)
	log.RemoveForbiddenWord(cloakConfig.CDNOriginHost)

	log.Debugf(Category, "Get mutex")
	if cloakConfig.RemoteHost != "" {
		common.Client.MarkInCriticalSection(Name)
		StopRoutingCloak(cloakConfig.RemoteHost)
		common.Client.MarkOutOffCriticalSection(Name)
		cloakConfig.RemoteHost = ""
	}

	if client == nil {
		return
	}

	log.Debugf(Category, "Start client disconnected")

	common.Client.Disconnect(Name)
	client = nil

	log.Debugf(Category, "Client disconnected")

	runtime.GC()
	debug.FreeOSMemory()
	log.Debugf(Category, "StopCloakClient: memory released")
}

func resolveRemoteHostIfNeeded(host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		log.Debugf(Category, "cloak client: RemoteHost '%s' is valid IPv4", host)
		return host, nil
	}
	if ip, ok := dnscache.LookupIPv4(host, "cloak"); ok {
		log.Debugf(Category, "cloak client: RemoteHost '%s' resolved from shared DNS cache -> %s", host, ip.String())
		return ip.String(), nil
	}

	log.Debugf(Category, "cloak client: RemoteHost '%s' cache miss -> resolving DNS timeout=%s...", host, remoteHostResolveTimeout)

	ip, err := dnscache.ResolveIPv4(context.Background(), host, remoteHostResolveTimeout, "cloak")
	if err != nil {
		return "", err
	}
	log.Debugf(Category, "cloak client: DNS resolved '%s' -> %s", host, ip.String())
	return ip.String(), nil
}
