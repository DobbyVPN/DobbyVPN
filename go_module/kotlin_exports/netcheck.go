package main

import (
	"C"
	"go_module/log"
	"go_module/netcheck"
)
import "fmt"

//export NetCheck
func NetCheck(configPath *C.char) *C.char {
	log.Infof("NetCheck")
	err := netcheck.NetCheck(C.GoString(configPath))
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
