package common

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"sync"
)

type NetworkConfig struct {
	TunGateway string // 10.X.Y.1
	TunDevice  string // 10.X.Y.2
}

var (
	networkConfigMu sync.Mutex
	networkConfig   *NetworkConfig
)

func GetNetworkConfig() (*NetworkConfig, error) {
	networkConfigMu.Lock()
	defer networkConfigMu.Unlock()
	if networkConfig == nil {
		config, err := selectNetworkConfig(localIPv4Addresses(), randomPrivateOctet)
		if err != nil {
			return nil, err
		}
		networkConfig = config
	}
	return networkConfig, nil
}

func selectNetworkConfig(assigned map[string]bool, nextOctet func() (int64, error)) (*NetworkConfig, error) {
	for i := 0; i < 20; i++ {
		x, err := nextOctet()
		if err != nil {
			break
		}
		y, err := nextOctet()
		if err != nil {
			break
		}

		gateway := fmt.Sprintf("10.%d.%d.1", x, y)
		device := fmt.Sprintf("10.%d.%d.2", x, y)

		if !assigned[gateway] && !assigned[device] {
			return &NetworkConfig{
				TunGateway: gateway,
				TunDevice:  device,
			}, nil
		}
	}

	fallback := &NetworkConfig{TunGateway: "10.255.255.1", TunDevice: "10.255.255.2"}
	if assigned[fallback.TunGateway] || assigned[fallback.TunDevice] {
		return nil, fmt.Errorf("cannot select a free TUN address pair: fallback %s or %s is already assigned", fallback.TunGateway, fallback.TunDevice)
	}
	return fallback, nil
}

func randomPrivateOctet() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(254))
	if err != nil {
		return 0, fmt.Errorf("failed to generate private network octet: %w", err)
	}
	return n.Int64() + 1, nil
}

// Keep addresses from readable interfaces if another adapter disappears mid-scan.
func localIPv4Addresses() map[string]bool {
	assigned := make(map[string]bool)
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			var ip net.IP
			switch addr := address.(type) {
			case *net.IPNet:
				ip = addr.IP
			case *net.IPAddr:
				ip = addr.IP
			}
			if ipv4 := ip.To4(); ipv4 != nil {
				assigned[ipv4.String()] = true
			}
		}
	}
	return assigned
}
