package cloak

import (
	"encoding/json"
	"github.com/cbeuw/Cloak/exported_client"
	"go_client/common"
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
	
	// Перехватываем panic на верхнем уровне
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
	
	// Перехватываем panic
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
