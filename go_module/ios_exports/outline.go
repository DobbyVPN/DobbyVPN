package cloak_outline

import (
	"fmt"
	"go_module/log"
	"go_module/outline"
	"runtime/debug"
)

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

func NewOutlineClient(transportConfig string, tunnelFD int, mtu int) (err error) {
	defer guardExport("NewOutlineClient")()
	log.Debugf(Category, "NewOutlineClient() called")

	if client != nil {
		if err := OutlineDisconnect(); err != nil {
			return fmt.Errorf("NewOutlineClient(): disconnect failed: %w", err)
		}
	}

	if tunnelFD < 0 {
		return fmt.Errorf("NewOutlineClient(): invalid tunnel fd %d", tunnelFD)
	}

	log.Debugf(Category, "Using tunnel fd=%d mtu=%d", tunnelFD, mtu)
	log.Debugf(Category, "Config length=%d", len(transportConfig))

	client = outline.NewClientWithFDAndOptions(transportConfig, tunnelFD, mtu, outline.ClientOptions{
		PreferTCPDNSForWebSocket: true,
	})

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

func OutlineStatus() (string, error) {
	defer guardExport("OutlineStatus")()

	if client == nil {
		return "client=false engineStarted=false fd=-1 deviceNil=true localProxyAlive=false reason=client_nil", nil
	}

	status := client.Status()
	log.Infof(Category, "OutlineStatus: %s", status)
	return status, nil
}
