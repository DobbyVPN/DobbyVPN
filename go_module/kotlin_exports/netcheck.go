package main

import "C"
import (
	"fmt"
	"go_module/netcheck"
	"strings"
)

//export NetCheck
func NetCheck(configPath string) *C.char {
	err := netcheck.NetCheck(strings.Clone(configPath))
	if err != nil {
		return C.CString(fmt.Sprintf("NetCheck error: %v", err))
	} else {
		return C.CString("")
	}
}

//export CancelNetCheck
func CancelNetCheck() {
	netcheck.CancelNetCheck()
}
