//go:build linux

package route

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func defaultGateway() (net.IP, netlink.Link, error) {
	routes, err := netlink.RouteListFiltered(
		netlink.FAMILY_V4,
		&netlink.Route{Table: unix.RT_TABLE_MAIN},
		netlink.RT_FILTER_TABLE,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("route list: %w", err)
	}
	for _, r := range routes {
		if r.Dst == nil && r.Gw != nil {
			l, _ := netlink.LinkByIndex(r.LinkIndex)
			return r.Gw, l, nil
		}
	}
	return nil, nil, errors.New("default gateway not found")
}

func InstallBypassRoutes(cidrs []*net.IPNet, table int) error {
	if len(cidrs) == 0 {
		return nil
	}
	gw, link, err := defaultGateway()
	if err != nil {
		return err
	}
	for _, n := range cidrs {
		r := &netlink.Route{
			Dst:       n,
			Gw:        gw,
			Table:     table,
			LinkIndex: link.Attrs().Index,
			Scope:     netlink.SCOPE_UNIVERSE,
		}
		if err := netlink.RouteAdd(r); err != nil && !isEEXIST(err) {
			return fmt.Errorf("add bypass route %s: %w", n, err)
		}
	}
	return nil
}

func isEEXIST(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, os.ErrExist):
		return true
	case errors.Is(err, unix.EEXIST):
		return true
	case strings.Contains(err.Error(), "file exists"):
		return true
	default:
		return false
	}
}
