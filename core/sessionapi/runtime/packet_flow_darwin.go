//go:build darwin && !ios

package runtime

import (
	"context"
	"io"

	"core/dnscache"
	"core/routing"
	"core/sessionapi"
	"core/tunnel"
	"core/tunnel/platform_engine"
	"core/tunnel/protected_dialer"
)

// NewPacketFlow runs the normal protocol readiness, health and cleanup
// lifecycle inside a Network Extension. macOS supplies the interface.
func NewPacketFlow(device *platform_engine.PacketFlowDevice, physical routing.MacOSPhysicalNetwork, factory DeviceFactory) sessionapi.Runtime {
	return New(Options{
		NewDevice: factory,
		NewCore: func(_ io.ReadWriteCloser, cache *dnscache.Cache) sessionCore {
			cache.SetResolver(physical.Identity, dnscache.MacOSLinkResolver(
				physical.IPv4, physical.IPv6, physical.DNSServers, physical.NAT64Prefixes))
			return &packetFlowCore{device: device, physical: physical}
		},
	})
}

type packetFlowCore struct {
	*tunnel.Engine
	device   *platform_engine.PacketFlowDevice
	physical routing.MacOSPhysicalNetwork
}

func (c *packetFlowCore) Connect(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	protected_dialer.SetPhysicalInterfaces(c.physical.IPv4, c.physical.IPv6)
	var err error
	c.Engine, err = tunnel.StartOwnedEngine(platform_engine.EngineConfig{PacketFlow: c.device})
	return err
}

func (c *packetFlowCore) Disconnect(ctx context.Context) error {
	if c.Engine != nil {
		if err := c.Engine.Stop(ctx); err != nil {
			return err
		}
		c.Engine = nil
	}
	protected_dialer.ResetDefaultRoute()
	return nil
}

func (*packetFlowCore) ProtocolParameters() (int, string) { return 0, "" }
