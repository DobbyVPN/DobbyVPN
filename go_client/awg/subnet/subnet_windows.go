//go:build windows

package subnet

import (
	"fmt"
	"go_client/awg/config"
	"go_client/log"
	"os"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"github.com/amnezia-vpn/amneziawg-windows/elevate"
	"github.com/amnezia-vpn/amneziawg-windows/tunnel"
	"github.com/amnezia-vpn/amneziawg-windows/version"
)

type SubnetData struct {
	InterfaceName string
	Config        *config.Config
	tdev          tun.Device
	bind          conn.Bind
}

func CreateSubnetData(tun string, conf *config.Config, tdev tun.Device, bind conn.Bind) *SubnetData {
	return &SubnetData{
		InterfaceName: tun,
		Config:        conf,
		tdev:          tdev,
		bind:          bind,
	}
}

func (subnet *SubnetData) ConfigureSubnet() error {
	log.Infof("[AWG] Configure subnet")

	log.Infof("[AWG] Getting current executable")
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Cannot get current executable: %v", err)
	}

	log.Infof("[AWG] CopyConfigOwnerToIPCSecurityDescriptor")
	err = tunnel.CopyConfigOwnerToIPCSecurityDescriptor(path)
	if err != nil {
		return fmt.Errorf("Cannot copy config owner to IPC security descriptor: %v", err)
	}

	log.Infof("[AWG] Starting %v", version.UserAgent())

	log.Infof("[AWG] Watching interface")
	watcher, err := watchInterface()
	if err != nil {
		return fmt.Errorf("Cannot watch interface: %v", err)
	}

	nativeTun := subnet.tdev.(*tun.NativeTun)

	log.Infof("[AWG] Enable firewall")
	err = enableFirewall(subnet.Config, nativeTun)
	if err != nil {
		return fmt.Errorf("Cannot enable firewall: %v", err)
	}

	log.Infof("[AWG] Dropping privileges")
	err = elevate.DropAllPrivileges(true)
	if err != nil {
		return fmt.Errorf("Cannot drop all privileges: %v", err)
	}

	log.Infof("[AWG] Watcher config")
	watcher.Configure(subnet.bind.(conn.BindSocketToInterface), subnet.Config, nativeTun)

	return nil
}
