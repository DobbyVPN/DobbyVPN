package common

import (
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"
)

type NetworkConfig struct {
	TunGateway string // 10.X.Y.1
	TunDevice  string // 10.X.Y.2
}

var (
	cfg  *NetworkConfig
	once sync.Once
)

func GetNetworkConfig() *NetworkConfig {
	once.Do(func() {
		cfg = generateConfig()
	})

	return cfg
}

func generateConfig() *NetworkConfig {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < 20; i++ {
		x := r.Intn(254) + 1
		y := r.Intn(254) + 1

		gateway := fmt.Sprintf("10.%d.%d.1", x, y)
		device := fmt.Sprintf("10.%d.%d.2", x, y)

		if isIPFree(gateway) && isIPFree(device) {
			return &NetworkConfig{
				TunGateway: gateway,
				TunDevice:  device,
			}
		}
	}

	return &NetworkConfig{
		TunGateway: "10.255.255.1",
		TunDevice:  "10.255.255.2",
	}
}

func isIPFree(target string) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return true
	}

	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()

		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}

			if ip.String() == target {
				return false
			}
		}
	}

	return true
}
