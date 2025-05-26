package cloak

import (
	"encoding/json"
	"github.com/cbeuw/Cloak/exported_client"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	"sync"
)

const Name = "cloak"

var (
	client *exported_client.CkClient
	mu     sync.Mutex
)

func InitLog() {
	exported_client.InitLog()

	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetLevel(log.InfoLevel)
}

func StartCloakClient(localHost, localPort, config string, udp bool) {
	mu.Lock()
	defer mu.Unlock()

	//if client != nil {
	//	log.Errorf("start cloak client failed: cloak client already started")
	//	return
	//}

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

	client = exported_client.NewCkClient(rawConfig)

	common.Client.SetVpnClient(Name, client)
	err = client.Connect() // TODO: handle err
	if err != nil {
		log.Errorf("cloak client: Failed to connect to cloak client - %v", err)
		return
	}

	log.Infof("cloak client connected")

	common.Client.MarkActive(Name)
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
