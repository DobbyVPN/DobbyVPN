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

func NewOutlineClient(transportConfig string, tunnelFD int, mtu int) (err error) {
	defer guardExport("NewOutlineClient")()
	log.Infof("NewOutlineClient() called")

	if client != nil {
		if err := OutlineDisconnect(); err != nil {
			return fmt.Errorf("NewOutlineClient(): disconnect failed: %w", err)
		}
	}

	if tunnelFD < 0 {
		return fmt.Errorf("NewOutlineClient(): invalid tunnel fd %d", tunnelFD)
	}

	log.Infof("Using tunnel fd=%d mtu=%d", tunnelFD, mtu)
	log.Infof("Config length=%d", len(transportConfig))

	client = outline.NewClientWithFD(transportConfig, tunnelFD, mtu)

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
		return nil
	}

	client.Disconnect()
	client = nil

	log.Infof("OutlineDisconnect() finished")
	return nil
}
