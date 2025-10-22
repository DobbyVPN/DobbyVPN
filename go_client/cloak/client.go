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

<<<<<<< HEAD
<<<<<<< HEAD
	common.Client.SetVpnClient(Name, client)
	err = common.Client.Connect(Name)
=======
	common.Client.SetVpnClient(exported_client.Name, client)
	err = common.Client.Connect(exported_client.Name)
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
	common.Client.SetVpnClient(Name, client)
	err = common.Client.Connect(Name)
>>>>>>> b44136a (Rollback status marking 2)
	if err != nil {
		log.Errorf("cloak client: Failed to connect to cloak client - %v", err)
		return
	}
<<<<<<< HEAD
<<<<<<< HEAD
	common.Client.MarkInProgress(Name)
	err = StartRoutingCloak(RemoteHostIP)
	if err != nil {
		common.Client.MarkInactive(Name)
		log.Infof("Can't routing cloak, %v", err)
		return
	}
	common.Client.MarkActive(Name)
=======
=======
	common.Client.MarkInProgress(Name)
>>>>>>> 7039ac7 (Rollback status marking)
	err = StartRoutingCloak(RemoteHostIP)
	if err != nil {
		common.Client.MarkInactive(Name)
		log.Infof("Can't routing cloak, %v", err)
		return
	}
<<<<<<< HEAD
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
	common.Client.MarkActive(Name)
>>>>>>> 7039ac7 (Rollback status marking)

	log.Infof("cloak client connected")
}

func StopCloakClient() {
	log.Infof("StopCloakClient inner")
<<<<<<< HEAD
<<<<<<< HEAD
	defer common.Client.MarkInactive(Name)
=======
	common.Client.MarkInProgress(exported_client.Name)
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
	defer common.Client.MarkInactive(Name)
>>>>>>> 7039ac7 (Rollback status marking)
	mu.Lock()
	defer mu.Unlock()
	log.Infof("Get mutex")
	if RemoteHostIP != "" {
		common.Client.MarkInProgress(Name)
		StopRoutingCloak(RemoteHostIP)
		common.Client.MarkActive(Name)
		RemoteHostIP = ""
	}

	if client == nil {
		return
	}

	log.Infof("Start client disconnected")

<<<<<<< HEAD
<<<<<<< HEAD
	common.Client.Disconnect(Name)
=======
	common.Client.Disconnect(exported_client.Name)
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
	common.Client.Disconnect(Name)
>>>>>>> b44136a (Rollback status marking 2)
	client = nil

	log.Infof("Client disconnected")
}
