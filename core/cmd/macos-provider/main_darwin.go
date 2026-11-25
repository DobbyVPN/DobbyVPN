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
	"fmt"
	"net"
	"net/netip"
	"strings"
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
	physicalIdentity string
	device           *platform_engine.PacketFlowDevice
	server           *macosprovider.Server
	ref              sessionapi.SessionRef
	ctx              context.Context
	cancel           context.CancelFunc
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
	physical, err := routing.CaptureMacOSProviderNetwork(ctx)
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
	state.active = &provider{physicalIdentity: physical.Identity, ref: ref, device: device, server: server, ctx: ctx, cancel: cancel}
	log.Info("MacPacketProvider", "Packet flow prepared", map[string]any{"session_id": ref.SessionID, "network_generation": ref.Generation,
		"network_identity": physical.Identity, "ipv4_interface": physical.IPv4, "ipv6_interface": physical.IPv6})
	return nil
}

//export DobbyProviderCheckUplink
func DobbyProviderCheckUplink() *C.char {
	state.RLock()
	defer state.RUnlock()
	if state.active == nil {
		return failure(errors.New("packet provider stopped"))
	}
	physical, err := routing.CaptureMacOSProviderNetwork(state.active.ctx)
	if err != nil {
		return failure(err)
	}
	if physical.Identity != state.active.physicalIdentity {
		return failure(fmt.Errorf("underlying macOS service changed from %s to %s", state.active.physicalIdentity, physical.Identity))
	}
	return nil
}

//export DobbyProviderRoutesReady
func DobbyProviderRoutesReady() *C.char {
	state.RLock()
	defer state.RUnlock()
	if state.active == nil {
		return failure(errors.New("missing packet flow during route activation"))
	}
	v4, v6 := platform_engine.MacOSTunnelAddresses()
	interfaces, err := net.Interfaces()
	if err != nil {
		return failure(err)
	}
	name := ""
	for _, link := range interfaces {
		if !strings.HasPrefix(link.Name, "utun") {
			continue
		}
		addrs, err := link.Addrs()
		if err != nil {
			return failure(err)
		}
		has4, has6 := false, false
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().String()
			has4 = has4 || ip == v4.Local
			has6 = has6 || (ip == v6.Local && prefix.Bits() == platform_engine.MacOSIPv6PrefixLength)
		}
		if has4 && has6 {
			if name != "" {
				return failure(fmt.Errorf("multiple interfaces have the configured packet flow addresses"))
			}
			name = link.Name
		}
	}
	if name == "" {
		return failure(errors.New("installed packet flow interface missing"))
	}
	owner := state.active.ref
	fields := map[string]any{
		"session_id": owner.SessionID, "network_generation": owner.Generation,
		"interface": name, "packet_provider": true, "ipv4": v4, "ipv6": v6,
		"requested_mtu": platform_engine.TunnelMTU, "requested_dns_servers": common.VPNDNSServers(),
	}
	if err := log.SetPolicyDetail(log.DesktopTunnelPolicyKey, fields); err != nil {
		log.Errorf("MacPacketProvider", "retain desktop tunnel policy: %v", err)
	}
	log.Info("MacPacketProvider", "macOS retained network ready", fields)
	return nil
}

//export DobbyProviderSettings
func DobbyProviderSettings() *C.char {
	v4, v6 := platform_engine.MacOSTunnelAddresses()
	settings := struct {
		IPv4, Mask, IPv6 string
		IPv6PrefixLength int
		DNS              []string
		MTU              int
	}{v4.Local, v4.Mask, v6.Local, platform_engine.MacOSIPv6PrefixLength, common.VPNDNSServers(), platform_engine.TunnelMTU}
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
