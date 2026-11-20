//go:build linux && !android

package dnscache

import (
	"context"

	"errors"
	"fmt"
	"github.com/miekg/dns"
	"math"
	"net"
	"net/netip"
	"strconv"
	"syscall"

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

type linuxDNSServer struct {
	Family  int32
	Address []byte
	Port    uint16
	Name    string
}
type linuxDNSLink struct {
	name     string
	identity string
	native   bool
	servers  []netip.AddrPort
}

// Read the link settings in one resolved reply. Include transport, validation,
// addresses and interface identity in the fence checked after every lookup.
func captureLinuxDNSLink(ctx context.Context, index int) (result linuxDNSLink, resultErr error) {
	if index <= 0 || index > math.MaxInt32 {
		return result, fmt.Errorf("invalid physical link %d", index)
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return result, err
	}
	local, err := iface.Addrs()
	if err != nil {
		return result, err
	}
	conn, link, err := openResolvedLink(ctx, int32(index))
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	var properties map[string]dbus.Variant
	if err := link.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.resolve1.Link").Store(&properties); err != nil {
		return result, err
	}
	tlsMode, tlsOK := properties["DNSOverTLS"].Value().(string)
	validation, validationOK := properties["DNSSEC"].Value().(string)
	if !tlsOK || !validationOK {
		return result, errors.New("physical DNS policy unavailable")
	}
	result.name = iface.Name
	result.native = tlsMode != "no" || validation != "no"
	var servers []linuxDNSServer
	extended, ok := properties["DNSEx"]
	if !ok {
		return result, errors.New("physical DNS server configuration unavailable")
	}
	if err := extended.Store(&servers); err != nil {
		return result, err
	}
	result.identity = fmt.Sprintf("linux:%s:%d:%s:%v:%s:%s:%s", iface.Name, index, iface.HardwareAddr, local, tlsMode, validation, extended.String())
	for _, server := range servers {
		ip, ok := netip.AddrFromSlice(server.Address)
		if !ok || ip.IsLoopback() || ip.IsUnspecified() || server.Family != unix.AF_INET && server.Family != unix.AF_INET6 {
			return result, errors.New("invalid underlying DNS address")
		}
		ip = ip.Unmap()
		if ip.Is6() && ip.IsLinkLocalUnicast() {
			ip = ip.WithZone(iface.Name)
		}
		port := server.Port
		if port == 0 {
			port = 53
		}
		result.servers = append(result.servers, netip.AddrPortFrom(ip, port))
		result.native = result.native || server.Name != "" || port == 853
	}
	return result, nil
}

// CaptureLinuxLink exposes the configured address inventory for diagnostics.
func CaptureLinuxLink(ctx context.Context, index int) (string, []netip.Addr, error) {
	link, err := captureLinuxDNSLink(ctx, index)
	if err != nil {
		return "", nil, err
	}
	servers := make([]netip.Addr, 0, len(link.servers))
	for _, server := range link.servers {
		servers = append(servers, server.Addr().WithZone(""))
	}
	return link.identity, servers, nil
}

// CaptureLinuxBootstrap uses backend-owned DNS sockets for plain DNS. Native
// DNSSEC and encrypted DNS retain resolved's configured validation policy.
func CaptureLinuxBootstrap(ctx context.Context, index, mark int, openNative func(context.Context, int, int, []netip.AddrPort) (func() error, error)) (string, Resolver, error) {
	link, err := captureLinuxDNSLink(ctx, index)
	if err != nil {
		return "", nil, err
	}
	upstreams := make([]string, 0, len(link.servers))
	for _, server := range link.servers {
		upstreams = append(upstreams, server.String())
	}
	lookup := func(ctx context.Context, host string) (addresses []net.IPAddr, err error) {
		if link.native {
			release, openErr := openNative(ctx, index, mark, link.servers)
			if openErr != nil {
				return nil, openErr
			}
			defer func() {
				if closeErr := release(); closeErr != nil {
					addresses = nil
					err = errors.Join(err, closeErr)
				}
			}()
			addresses, err = resolveOnLink(ctx, int32(index), host)
			if err != nil {
				return nil, err
			}
		} else {
			dialer := net.Dialer{Control: func(_, _ string, raw syscall.RawConn) error {
				var socketErr error
				err := raw.Control(func(fd uintptr) {
					socketErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, link.name)
					if socketErr == nil {
						socketErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
					}
				})
				return errors.Join(err, socketErr)
			}}
			addresses, err = resolveCapturedDNSServers(ctx, host, upstreams, &dialer, link.identity, []uint16{dns.TypeA, dns.TypeAAAA})
			if err != nil {
				return nil, err
			}
		}
		current, err := captureLinuxDNSLink(ctx, index)
		if err != nil || current.identity != link.identity {
			return nil, errors.Join(err, errors.New("physical DNS configuration changed during lookup"))
		}
		return addresses, nil
	}
	return link.identity, lookup, nil
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
