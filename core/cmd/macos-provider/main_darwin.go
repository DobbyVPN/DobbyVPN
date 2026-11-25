//go:build darwin && !ios

package main

/*
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"unsafe"

	"core/common"
	"core/log"
	"core/routing"
	"core/sessionapi"
	"core/sessionapi/macosprovider"
	"core/sessionapi/runtimebridge"
	"core/tunnel/platform_engine"
)

type provider struct {
	device *platform_engine.PacketFlowDevice
	server *macosprovider.Server
	ctx    context.Context
	cancel context.CancelFunc
}

var state struct {
	sync.RWMutex
	active *provider
}

func failure(err error) *C.char {
	if err == nil {
		return nil
	}
	log.Errorf("MacPacketProvider", "%v", err)
	return C.CString(err.Error())
}

//export DobbyProviderStart
func DobbyProviderStart(key, session *C.char, generation C.uint64_t) *C.char {
	state.Lock()
	defer state.Unlock()
	if state.active != nil {
		return failure(errors.New("packet provider already running"))
	}
	if err := log.SetAppenderPath("/Library/Logs/DobbyVPN/backend.jsonl"); err != nil {
		return failure(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	physical, err := routing.CaptureMacOSPhysicalNetwork(ctx)
	if err != nil {
		cancel()
		return failure(err)
	}
	device := platform_engine.NewPacketFlowDevice()
	ref := sessionapi.SessionRef{SessionID: C.GoString(session), Generation: uint64(generation)}
	server, err := macosprovider.NewServer(ctx, C.GoString(key), ref, runtimebridge.NewPacketFlow(device, physical))
	if err != nil {
		cancel()
		device.Close()
		return failure(err)
	}
	state.active = &provider{device: device, server: server, ctx: ctx, cancel: cancel}
	log.Info("MacPacketProvider", "Packet flow prepared", map[string]any{"session_id": ref.SessionID, "network_generation": ref.Generation,
		"network_identity": physical.Identity, "ipv4_interface": physical.IPv4, "ipv6_interface": physical.IPv6})
	return nil
}

//export DobbyProviderSettings
func DobbyProviderSettings() *C.char {
	v4, v6 := platform_engine.MacOSTunnelAddresses()
	settings := struct {
		IPv4, Mask, IPv6 string
		DNS              []string
		MTU              int
	}{v4.Local, v4.Mask, v6.Local, common.VPNDNSServers(), platform_engine.TunnelMTU}
	data, _ := json.Marshal(settings)
	return C.CString(string(data))
}

//export DobbyProviderReceive
func DobbyProviderReceive(version C.uint8_t, data *C.uint8_t, length C.size_t) C.int {
	if data == nil || length == 0 || length > platform_engine.TunnelMTU {
		return 0
	}
	state.RLock()
	owner := state.active
	state.RUnlock()
	if owner == nil || owner.device.Receive(uint8(version), unsafe.Slice((*byte)(data), int(length))) != nil {
		return 0
	}
	return 1
}

//export DobbyProviderRead
func DobbyProviderRead(data *C.uint8_t, capacity C.size_t, version *C.uint8_t) C.int {
	if data == nil || version == nil || capacity < platform_engine.TunnelMTU {
		return 0
	}
	state.RLock()
	owner := state.active
	state.RUnlock()
	if owner == nil {
		return 0
	}
	family, packet, err := owner.device.ReadPacket(owner.ctx)
	if err != nil {
		return 0
	}
	copy(unsafe.Slice((*byte)(data), int(capacity)), packet)
	*version = C.uint8_t(family)
	return C.int(len(packet))
}

//export DobbyProviderStop
func DobbyProviderStop() *C.char {
	state.Lock()
	defer state.Unlock()
	owner := state.active
	if owner == nil {
		return nil
	}
	owner.cancel()
	if err := owner.server.Close(context.Background()); err != nil {
		return failure(err)
	}
	state.active = nil
	log.Infof("MacPacketProvider", "Packet flow released")
	return nil
}

func main() {}
