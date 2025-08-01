package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"
import "go_client/awg"

//export AwgTurnOn
func AwgTurnOn(interfaceName string, tunFd int32, settings string) int32 {
	return awg.AwgTurnOn(interfaceName, tunFd, settings)
}

//export AwgTurnOff
func AwgTurnOff(tunnelHandle int32) {
	awg.AwgTurnOff(tunnelHandle)
}

//export AwgGetSocketV4
func AwgGetSocketV4(tunnelHandle int32) int32 {
	return awg.AwgGetSocketV4(tunnelHandle)
}

//export AwgGetSocketV6
func AwgGetSocketV6(tunnelHandle int32) int32 {
	return awg.AwgGetSocketV6(tunnelHandle)
}

//export AwgGetConfig
func AwgGetConfig(tunnelHandle int32) string {
	return awg.AwgGetConfig(tunnelHandle)
}

//export AwgVersion
func AwgVersion() string {
	return awg.AwgVersion()
}
