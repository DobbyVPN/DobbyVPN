package internal

type App struct {
	VlessConfig   *string
	RoutingConfig *RoutingConfig
}

type RoutingConfig struct {
	TunDeviceName        string
	TunDeviceIP          string
	TunDeviceMTU         int
	TunGatewayCIDR       string
	RoutingTableID       int
	RoutingTablePriority int
	DNSServerIP          string
}

