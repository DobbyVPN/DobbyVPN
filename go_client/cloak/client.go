package cloak

import (
	"encoding/json"
	"github.com/cbeuw/Cloak/exported_client"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	"sync"

	_ "go_client/logger"
)

const Name = "cloak"

var (
	client *exported_client.CkClient
	mu     sync.Mutex
)

func StartCloakClient(localHost, localPort, config string, udp bool) error {
	mu.Lock()
	defer mu.Unlock()

	if client != nil {
		StopCloakClient()
	}

	var rawConfig exported_client.Config
	err := json.Unmarshal([]byte(config), &rawConfig)
	if err != nil {
		log.Errorf("cloak client: Failed to unmarshal config - %v", err)
		return err
	}
	log.Infof("cloak client: rawConfig parsed successfully: %+v", rawConfig)

	rawConfig.LocalHost = localHost
	rawConfig.LocalPort = localPort
	rawConfig.UDP = udp
	log.Infof("cloak client: rawConfig updated with LocalHost=%s, LocalPort=%s, UDP=%v", localHost, localPort, udp)

	client = exported_client.NewCkClient(rawConfig)

	common.Client.SetVpnClient(Name, client)
	err = client.Connect()
	if err != nil {
		log.Errorf("cloak client: Failed to connect to cloak client - %v", err)
		return err
	}

	log.Infof("cloak client connected")

	common.Client.MarkActive(Name)
	return nil
}

func StopCloakClient() {
	defer common.Client.MarkInactive(Name)
	mu.Lock()
	defer mu.Unlock()

	if client == nil {
		return
	}

	client.Disconnect()
	client = nil
}
