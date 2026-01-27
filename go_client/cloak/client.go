package cloak

import (
	"encoding/json"
	"fmt"
	"github.com/cbeuw/Cloak/exported_client"
	"go_client/common"
	"net"
	"sync"

	log "go_client/logger"
)

const Name = "cloak"

var (
	client      *exported_client.CkClient
	mu          sync.Mutex
	cloakConfig exported_client.Config
)

func StartCloakClient(localHost, localPort, config string, udp bool) {
	log.Infof("StartCloakClient inner")

	// Handle panic
	defer func() {
		if r := recover(); r != nil {
			log.Infof("StartCloakClient: recovered from panic: %v", r)
		}
	}()

	mu.Lock()
	defer mu.Unlock()

	log.Infof("Get lock")

	if client != nil {
		log.Infof("Need to stop old cloak client")
		mu.Unlock()
		StopCloakClient()
		mu.Lock()
	}

	log.Infof("deleted old cloak client")

	var rawConfig exported_client.Config
	err := json.Unmarshal([]byte(config), &rawConfig)
	if err != nil {
		log.Infof("cloak client: Failed to unmarshal config - %v", err)
		return
	}
	log.Infof("cloak client: rawConfig parsed successfully")

	rawConfig.RemoteHost, err = resolveRemoteHostIfNeeded(rawConfig.RemoteHost)
	if err != nil {
		log.Infof("Can't resolve Remote Host: %v", err)
		return
	}
	rawConfig.LocalHost = localHost
	rawConfig.LocalPort = localPort
	rawConfig.UDP = udp
	log.Infof("cloak client: rawConfig updated with LocalHost=%s, LocalPort=%s, UDP=%v", localHost, localPort, udp)

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
		log.Infof("Can't routing cloak, %v", err)
		return
	}
	log.Infof("cloak client: Routed")

	client = exported_client.NewCkClient(rawConfig)

	common.Client.SetVpnClient(Name, client)
	err = common.Client.Connect(Name)
	if err != nil {
		log.Infof("cloak client: Failed to connect to cloak client - %v", err)
		return
	}

	log.Infof("cloak client connected")

	common.Client.MarkActive(Name)
}

func StopCloakClient() {
	log.Infof("StopCloakClient inner")

	// Handle panic
	defer func() {
		if r := recover(); r != nil {
			log.Infof("StopCloakClient: recovered from panic: %v", r)
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

	log.Infof("Get mutex")
	if cloakConfig.RemoteHost != "" {
		common.Client.MarkInCriticalSection(Name)
		StopRoutingCloak(cloakConfig.RemoteHost)
		common.Client.MarkOutOffCriticalSection(Name)
		cloakConfig.RemoteHost = ""
	}

	if client == nil {
		return
	}

	log.Infof("Start client disconnected")

	common.Client.Disconnect(Name)
	client = nil

	log.Infof("Client disconnected")
}

func resolveRemoteHostIfNeeded(host string) (string, error) {
	if net.ParseIP(host) != nil {
		log.Infof("cloak client: RemoteHost '%s' is valid IPv4", host)
		return host, nil
	}

	log.Infof("cloak client: RemoteHost '%s' is not IPv4 -> resolving DNS...", host)

	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("DNS resolve failed: %w", err)
	}

	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			log.Infof("cloak client: DNS resolved '%s' -> %s", host, v4.String())
			return v4.String(), nil
		}
	}

	return "", fmt.Errorf("DNS resolved only IPv6, IPv4 required")
}
