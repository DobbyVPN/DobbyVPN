//go:build linux && !android

package dnscache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

// UseInterface selects resolved's physical-link resolver before VPN DNS is
// installed. A loopback stub and the global VPN routing domain are never used.
func (c *Cache) UseInterface(index int) error {
	if index <= 0 || index > math.MaxInt32 {
		return fmt.Errorf("invalid underlying interface index %d", index)
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return err
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return err
	}
	linkIndex := int32(index)
	identity := fmt.Sprintf("%s:%d:%v", iface.Name, index, addresses)
	c.SetResolver(identity, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return resolveOnLink(ctx, linkIndex, host)
	})
	return nil
}

type linkAddress struct {
	Index   int32
	Family  int32
	Address []byte
}

func resolveOnLink(ctx context.Context, index int32, host string) (addresses []net.IPAddr, resultErr error) {
	conn, link, err := openResolvedLink(ctx, index)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	manager := conn.Object("org.freedesktop.resolve1", "/org/freedesktop/resolve1")
	// Retain the per-link DNS identity around the query. Native cache updates
	// belong to resolved; an answer racing link reconfiguration is discarded.
	var before, after dbus.Variant
	if err := link.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.resolve1.Link", "DNS").Store(&before); err != nil {
		return nil, err
	}
	var records []linkAddress
	var canonical string
	var flags uint64
	if err := manager.CallWithContext(ctx, "org.freedesktop.resolve1.Manager.ResolveHostname", 0, index, host, int32(unix.AF_UNSPEC), uint64(0)).Store(&records, &canonical, &flags); err != nil {
		return nil, err
	}
	if err := link.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.resolve1.Link", "DNS").Store(&after); err != nil {
		return nil, err
	}
	if before.String() != after.String() {
		return nil, errors.New("underlying DNS changed during lookup")
	}
	for _, record := range records {
		if record.Index != 0 && record.Index != index {
			return nil, fmt.Errorf("resolver returned an address from interface %d, expected %d", record.Index, index)
		}
		if (record.Family != unix.AF_INET && record.Family != unix.AF_INET6) || record.Family == unix.AF_INET && len(record.Address) != net.IPv4len || record.Family == unix.AF_INET6 && len(record.Address) != net.IPv6len {
			return nil, errors.New("invalid native resolver address length")
		}
		ip := net.IP(record.Address)
		address := net.IPAddr{IP: ip}
		if ip.To4() == nil && ip.IsLinkLocalUnicast() {
			address.Zone = strconv.Itoa(int(index))
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

// CaptureLinuxLink reads only the selected physical link before application DNS
// takes ownership of the VPN link. Returned servers authorize bootstrap packets.
func CaptureLinuxLink(ctx context.Context, index int) (identity string, servers []netip.Addr, resultErr error) {
	if index <= 0 || index > math.MaxInt32 {
		return "", nil, fmt.Errorf("invalid physical link %d", index)
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return "", nil, err
	}
	local, err := iface.Addrs()
	if err != nil {
		return "", nil, err
	}
	conn, link, err := openResolvedLink(ctx, int32(index))
	if err != nil {
		return "", nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	var value dbus.Variant
	if err := link.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.resolve1.Link", "DNS").Store(&value); err != nil {
		return "", nil, err
	}
	var native []struct {
		Family  int32
		Address []byte
	}
	if err := dbus.Store([]any{value.Value()}, &native); err != nil {
		return "", nil, err
	}
	for _, record := range native {
		address, ok := netip.AddrFromSlice(record.Address)
		if !ok || (record.Family != unix.AF_INET && record.Family != unix.AF_INET6) || address.IsLoopback() || address.IsUnspecified() {
			return "", nil, fmt.Errorf("invalid underlying DNS address: %v", record)
		}
		servers = append(servers, address.Unmap())
	}
	return fmt.Sprintf("linux:%s:%d:%s:%v:%v", iface.Name, index, iface.HardwareAddr, local, servers), servers, nil
}

func LinuxLinkResolver(index int) Resolver {
	if index <= 0 || index > math.MaxInt32 {
		return func(context.Context, string) ([]net.IPAddr, error) {
			return nil, fmt.Errorf("invalid underlying interface index %d", index)
		}
	}
	linkIndex := int32(index)
	return func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return resolveOnLink(ctx, linkIndex, host)
	}
}

func openResolvedLink(ctx context.Context, index int32) (*dbus.Conn, dbus.BusObject, error) {
	socket, err := (&net.Dialer{}).DialContext(ctx, "unix", "/run/dbus/system_bus_socket")
	if err != nil {
		return nil, nil, fmt.Errorf("connect underlying resolver bus: %w", err)
	}
	conn, err := dbus.NewConn(socket, dbus.WithContext(ctx))
	if err != nil {
		return nil, nil, errors.Join(err, socket.Close())
	}
	fail := func(err error) (*dbus.Conn, dbus.BusObject, error) { return nil, nil, errors.Join(err, conn.Close()) }
	if err := conn.Auth(nil); err != nil {
		return fail(err)
	}
	if err := conn.Hello(); err != nil {
		return fail(err)
	}
	var path dbus.ObjectPath
	if err := conn.Object("org.freedesktop.resolve1", "/org/freedesktop/resolve1").CallWithContext(ctx, "org.freedesktop.resolve1.Manager.GetLink", 0, index).Store(&path); err != nil {
		return fail(err)
	}
	object := conn.Object("org.freedesktop.resolve1", path)
	return conn, object, nil
}
