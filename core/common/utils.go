package common

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"sync"
)

var (
	tunAddressMu sync.Mutex
	tunAddress   string
)

// TUNDeviceIPv4 returns the process's first successfully selected device address.
func TUNDeviceIPv4() (string, error) {
	tunAddressMu.Lock()
	defer tunAddressMu.Unlock()
	if tunAddress != "" {
		return tunAddress, nil
	}
	address, err := selectTUNDeviceIPv4(localIPv4Addresses(), randomPrivateOctet)
	if err == nil {
		tunAddress = address
	}
	return address, err
}

func selectTUNDeviceIPv4(assigned map[string]bool, nextOctet func() (int64, error)) (string, error) {
	for i := 0; i < 20; i++ {
		x, err := nextOctet()
		if err != nil {
			break
		}
		y, err := nextOctet()
		if err != nil {
			break
		}

		address := fmt.Sprintf("10.%d.%d.2", x, y)
		if !assigned[address] {
			return address, nil
		}
	}

	const fallback = "10.255.255.2"
	if assigned[fallback] {
		return "", fmt.Errorf("cannot select a free TUN address: fallback %s is already assigned", fallback)
	}
	return fallback, nil
}

func randomPrivateOctet() (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(254))
	if err != nil {
		return 0, err
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
