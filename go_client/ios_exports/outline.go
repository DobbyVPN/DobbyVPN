//go:build android || ios

package cloak_outline

import (
	"fmt"
	log "go_client/logger"
	"go_client/outline"
	"runtime/debug"

	"golang.org/x/sys/unix"
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
		return fmt.Sprintf("%v", v)
	}
}

func NewOutlineClient(transportConfig string) (err error) {
	defer guardExport("NewOutlineClient")()
	log.Infof("NewOutlineClient() called")

	// если клиент уже был — корректно закрываем
	if client != nil {
		if err := OutlineDisconnect(); err != nil {
			return fmt.Errorf("NewOutlineClient(): disconnect failed: %w", err)
		}
	}

	log.Infof("Start fd search")
	fd := GetTunnelFileDescriptor()
	if fd < 0 {
		return fmt.Errorf("NewOutlineClient(): utun fd not found")
	}

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
		return fmt.Errorf("OutlineConnect(): client is nil")
	}

	if err := client.Connect(); err != nil {
		log.Infof("OutlineConnect() failed: %v", err)
		return fmt.Errorf("OutlineConnect(): %w", err)
	}

	log.Infof("OutlineConnect() finished successfully")
	return nil
}

func OutlineDisconnect() error {
	defer guardExport("OutlineDisconnect")()
	log.Infof("OutlineDisconnect() called")

	if client == nil {
		// это не ошибка — просто нечего отключать
		return nil
	}

	client.Disconnect()
	client = nil

	log.Infof("OutlineDisconnect() finished")
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
