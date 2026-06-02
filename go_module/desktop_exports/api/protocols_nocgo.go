//go:build !cgo && !(android || ios)

package api

func GetVpnLastError() string {
	return getVpnLastError()
}

func StartVpn(config, protocol string) int32 {
	return startVpn(config, protocol)
}

func StopVpn() {
	stopVpn()
}
