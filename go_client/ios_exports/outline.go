package cloak_outline

import "C"
import (
	"fmt"
	log "go_client/logger"
	"go_client/outline"
	"runtime/debug"
)

const utunControlName = "com.apple.net.utun_control"

var client *outline.OutlineClient

func guardExport(fnName string) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			log.Infof("%s\n%s", msg, string(debug.Stack()))
		}
	}
}

func unsafeToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return "non-string panic"
	}
}

func NewOutlineClient(transportConfig string) (err error) {
	defer guardExport("NewOutlineClient")()
	log.Infof("NewOutlineClient() called")
	err = OutlineDisconnect()
	if err != nil {
		return fmt.Errorf("NewOutlineClient() failed: %v", err)
	}
	log.Infof("Start fd search")
    fd := GetTunnelFileDescriptor()
	log.Infof("Fd was found, fd = %d", fd)
	log.Infof("Config length=%d", len(transportConfig))
	client = outline.NewClient(transportConfig, fd)
	log.Infof("NewOutlineClient() finished")
	return nil
}

func OutlineConnect() error {
	defer guardExport("OutlineConnect")()
	log.Infof("OutlineConnect() called")
	if client == nil {
		return fmt.Errorf("OutlineConnect() failed: client is nil")
	}
	err := client.Connect()
	if err != nil {
		log.Infof("OutlineConnect() failed: %v", err)
		return fmt.Errorf("OutlineConnect() failed: %v", err)
	}
	log.Infof("OutlineConnect() finished successfully")
	return nil
}

func OutlineDisconnect() error {
	defer guardExport("OutlineDisconnect")()
	log.Infof("OutlineDisconnect() called")
	if client == nil {
		return fmt.Errorf("OutlineDisconnect(): client is nil")
	}
	client.Disconnect()
	log.Infof("OutlineDisconnect() finished")
	return nil
}


func GetTunnelFileDescriptor() int32 {
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], utunControlName)
	for fd := 0; fd < 1024; fd++ {
		addr, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}
		addrCTL, loaded := addr.(*unix.SockaddrCtl)
		if !loaded {
			continue
		}
		if ctlInfo.Id == 0 {
			err = unix.IoctlCtlInfo(fd, ctlInfo)
			if err != nil {
				continue
			}
		}
		if addrCTL.ID == ctlInfo.Id {
			return int32(fd)
		}
	}
	return -1
}