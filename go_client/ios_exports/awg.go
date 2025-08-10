package cloak_outline

import "go_client/awg"

func AwgTurnOn(interfaceName string, tunFd int32, settings string) int32 {
	return awg.AwgTurnOn(interfaceName, tunFd, settings)
}

func AwgTurnOff(tunnelHandle int32) {
	awg.AwgTurnOff(tunnelHandle)
}

func AwgGetSocketV4(tunnelHandle int32) int32 {
	return awg.AwgGetSocketV4(tunnelHandle)
}

func AwgGetSocketV6(tunnelHandle int32) int32 {
	return awg.AwgGetSocketV6(tunnelHandle)
}

func AwgGetConfig(tunnelHandle int32) string {
	return awg.AwgGetConfig(tunnelHandle)
}

func AwgVersion() string {
	return awg.AwgVersion()
}
