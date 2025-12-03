//go:build darwin
// +build darwin

package internal

import (
	"errors"
	"fmt"
	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/songgao/water"
	"log"
	"os/exec"
	"os/user"

	"go_client/routing"
)

func checkRoot() bool {
	user, err := user.Current()
	if err != nil {
		log.Printf("Failed to get current user")
		return false
	}
	return user.Uid == "0"
}

type tunDevice struct {
	*water.Interface
	name string
}

var _ network.IPDevice = (*tunDevice)(nil)

func newTunDevice(name, ip string) (d network.IPDevice, err error) {
	if !checkRoot() {
		return nil, errors.New("this operation requires superuser privileges. Please run the program with sudo or as root")
	}

	if len(name) == 0 {
		return nil, errors.New("name is required for TUN/TAP device")
	}
	if len(ip) == 0 {
		return nil, errors.New("ip is required for TUN/TAP device")
	}

	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN/TAP device: %w", err)
	}

	log.Printf("Successfully created TUN/TAP device\n")

	defer func() {
		if err != nil {
			tun.Close()
		}
	}()

	log.Printf("Tun successful")

	tunDev := &tunDevice{tun, tun.Name()}

	addTunRoute := fmt.Sprintf("sudo ifconfig %s inet 169.254.19.0 169.254.19.0 netmask 255.255.255.0", tun.Name())
	if _, err := routing.ExecuteCommand(addTunRoute); err != nil {
		return nil, fmt.Errorf("failed to add tun route: %w", err)
	}

	// Uncomment and implement if needed
	//if err := tunDev.configureSubnet(ip); err != nil {
	//	return nil, fmt.Errorf("failed to configure TUN/TAP device subnet: %w", err)
	//}
	//if err := tunDev.bringUp(); err != nil {
	//	return nil, fmt.Errorf("failed to bring up TUN/TAP device: %w", err)
	//}
	log.Printf("TUN device %s is configured with IP %s\n", tunDev.Interface.Name(), "10.0.85.2")
	return tunDev, nil
}

func (d *tunDevice) MTU() int {
	return 1500
}

func (d *tunDevice) configureSubnet(ip string) error {
	fmt.Printf("Configuring subnet for TUN device %s with IP %s\n", d.name, ip)
	cmd := exec.Command("ifconfig", d.name, ip, "netmask", "255.255.255.0", "up")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to configure subnet: %w, output: %s", err, output)
	}

	fmt.Printf("Subnet configuration completed for TUN device %s\n", d.name)
	return nil
}

func (d *tunDevice) bringUp() error {
	fmt.Printf("Bringing up TUN device %s\n", d.name)
	cmd := exec.Command("ifconfig", d.name, "up")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to bring up device: %w, output: %s", err, output)
	}
	fmt.Printf("TUN device %s is now active\n", d.name)
	return nil
}
