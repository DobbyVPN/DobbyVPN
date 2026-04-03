package tunnel

import (
	"go_client/awg/config"
	"net"
	"os"

	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig *config.Config
	logger          *device.Logger
	tdev            tun.Device
	dev             *device.Device
	uapiListener    net.Listener
	errs            chan error
	term            chan os.Signal
}
