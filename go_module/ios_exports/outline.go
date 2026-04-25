package cloak_outline

import (
	"fmt"
	"go_module/log"
	"go_module/outline"
	"os"
	"runtime/debug"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"

var client *outline.OutlineClient

func guardExport(fnName string) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			log.Debugf(Category, "%s\n%s", msg, string(debug.Stack()))
		}
	}
}

func unsafeToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

func NewOutlineClient(transportConfig string) (err error) {
	defer guardExport("NewOutlineClient")()
	log.Debugf(Category, "NewOutlineClient() called")

	if client != nil {
		if err := OutlineDisconnect(); err != nil {
			return fmt.Errorf("NewOutlineClient(): disconnect failed: %w", err)
		}
	}

	log.Debugf(Category, "Start fd search")

	fd := GetTunnelFileDescriptor()
	if fd < 0 {
		return fmt.Errorf("NewOutlineClient(): utun fd not found")
	}

	log.Debugf(Category, "Fd was found, fd = %d", fd)
	log.Debugf(Category, "Config length=%d", len(transportConfig))

	tunFile := os.NewFile(uintptr(fd), "utun")

	client = outline.NewClient(transportConfig, tunFile)

	log.Infof(Category, "NewOutlineClient() finished")
	return nil
}

func OutlineConnect() error {
	defer guardExport("OutlineConnect")()
	log.Debugf(Category, "OutlineConnect() called")

	if client == nil {
		return fmt.Errorf("OutlineConnect(): client is nil")
	}

	if err := client.Connect(); err != nil {
		log.Errorf(Category, "OutlineConnect() failed: %v", err)
		return fmt.Errorf("OutlineConnect(): %w", err)
	}

	log.Infof(Category, "OutlineConnect() finished successfully")
	return nil
}

func OutlineDisconnect() error {
	defer guardExport("OutlineDisconnect")()
	log.Debugf(Category, "OutlineDisconnect() called")

	if client == nil {
		return nil
	}

	client.Disconnect()
	client = nil

	log.Infof(Category, "OutlineDisconnect() finished")
	return nil
}

func GetTunnelFileDescriptor() int {
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], utunControlName)

	for fd := 0; fd < 1024; fd++ {
		addr, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}

		addrCTL, ok := addr.(*unix.SockaddrCtl)
		if !ok {
			continue
		}

		if ctlInfo.Id == 0 {
			if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
				continue
			}
		}

		if addrCTL.ID == ctlInfo.Id {
			return fd
		}
	}

	return -1
}
