package cloak

import (
	"encoding/json"
	"github.com/cbeuw/Cloak/exported_client"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	"sync"

	_ "go_client/logger"
)

var (
	client       *exported_client.CkClient
	mu           sync.Mutex
	RemoteHostIP string
)

func InitLog() {
	exported_client.InitLog()

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.InfoLevel)
}

func StartCloakClient(localHost, localPort, config string, udp bool) {
	log.Infof("StartCloakClient inner")
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
		log.Errorf("cloak client: Failed to unmarshal config - %v", err)
		return
	}
	log.Infof("cloak client: rawConfig parsed successfully: %+v", rawConfig)

	rawConfig.LocalHost = localHost
	rawConfig.LocalPort = localPort
	rawConfig.UDP = udp
	log.Infof("cloak client: rawConfig updated with LocalHost=%s, LocalPort=%s, UDP=%v", localHost, localPort, udp)

	// Cloak routing
	RemoteHostIP = rawConfig.RemoteHost

	client = exported_client.NewCkClient(rawConfig)

	common.Client.SetVpnClient(exported_client.Name, client)
	common.Client.MarkInProgress(exported_client.Name)
	err = common.Client.Connect(exported_client.Name)
	if err != nil {
		log.Errorf("cloak client: Failed to connect to cloak client - %v", err)
		return
	}
	err = StartRoutingCloak(RemoteHostIP)
	if err != nil {
		log.Infof("Can't routing cloak, %v", err)
		return
	}

	log.Infof("cloak client connected")
}

func StopCloakClient() {
	log.Infof("StopCloakClient inner")
	common.Client.MarkInProgress(exported_client.Name)
	mu.Lock()
	defer mu.Unlock()
	log.Infof("Get mutex")
	if RemoteHostIP != "" {
		StopRoutingCloak(RemoteHostIP)
		RemoteHostIP = ""
	}

	if client == nil {
		return
	}

	log.Infof("Start client disconnected")

	common.Client.Disconnect(exported_client.Name)
	client = nil

	log.Infof("Client disconnected")
}
