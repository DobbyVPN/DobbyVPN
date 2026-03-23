//go:build darwin
// +build darwin

package internal

import (
	"errors"
	"fmt"
	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/songgao/water"
	"go_client/log"
	"os"
	"os/exec"
	"os/user"
	"reflect"

	"go_client/routing"
)

func checkRoot() bool {
	user, err := user.Current()
	if err != nil {
		log.Infof("Failed to get current user")
		return false
	}
	return user.Uid == "0"
}

type tunDevice struct {
	*water.Interface
	name string
	fd   int
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

	log.Infof("Successfully created TUN/TAP device\n")

	defer func() {
		if err != nil {
			tun.Close()
		}
	}()

	fd := extractFD(tun)

	log.Infof("Tun successful")

	tunDev := &tunDevice{
		Interface: tun,
		name:      tun.Name(),
		fd:        fd,
	}

	addTunRoute := fmt.Sprintf("sudo ifconfig %s inet 169.254.19.0 169.254.19.0 netmask 255.255.255.0", tun.Name())
	if _, err := routing.ExecuteCommand(addTunRoute); err != nil {
		return nil, fmt.Errorf("failed to add tun route: %w", err)
	}

	log.Infof("TUN device %s is configured with IP %s\n", tunDev.Interface.Name(), "10.0.85.2")
	return tunDev, nil
}

func (d *tunDevice) MTU() int {
	return 1500
}

func (d *tunDevice) configureSubnet(ip string) error {
	log.Infof("Configuring subnet for TUN device %s with IP %s\n", d.name, ip)
	cmd := exec.Command("ifconfig", d.name, ip, "netmask", "255.255.255.0", "up")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to configure subnet: %w, output: %s", err, output)
	}

	log.Infof("Subnet configuration completed for TUN device %s\n", d.name)
	return nil
}

func (d *tunDevice) bringUp() error {
	log.Infof("Bringing up TUN device %s\n", d.name)
	cmd := exec.Command("ifconfig", d.name, "up")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to bring up device: %w, output: %s", err, output)
	}
	log.Infof("TUN device %s is now active\n", d.name)
	return nil
}

func extractFD(iface *water.Interface) int {
	rwc := iface.ReadWriteCloser

	val := reflect.ValueOf(rwc)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	fileField := val.FieldByName("file")
	if !fileField.IsValid() {
		return -1
	}

	file := fileField.Interface().(*os.File)
	return int(file.Fd())
}

func (d *tunDevice) GetFd() int {
	return d.GetFd()
}
