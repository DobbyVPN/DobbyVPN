//go:build darwin

package subnet

import "go_client/awg/config"

type SubnetData struct {
	InterfaceName string
	Config        *config.Config
}

func CreateSubnetData(tun string, conf *config.Config) *SubnetData {
	return &SubnetData{
		InterfaceName: tun,
		Config:        conf,
	}
}

func (subnet *SubnetData) ConfigureSubnet() error {
	return nil
}
