//go:build linux && !android

package routing

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	nl "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"os"
	"os/user"
	"strconv"
	"sync/atomic"
)

var resolverQueries atomic.Uint64

// OpenLinuxResolverQuery authorizes resolved only for a backend-requested
// native lookup on the physical link. Closing its netlink socket, including
// on service death, revokes the temporary rules independently of the base.
func OpenLinuxResolverQuery(ctx context.Context, index, mark int, servers []netip.AddrPort) (func() error, error) {
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return nil, err
	}
	local, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	account, err := user.Lookup("systemd-resolve")
	if err != nil {
		return nil, err
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, err
	}
	g := &LinuxOutputGuard{mark: uint32(mark)}
	g.conn, err = nftables.New(nftables.AsLasting(), nftables.WithSockOptions(func(socket *nl.Conn) error { g.socket = socket; return nil }))
	if err != nil {
		return nil, err
	}
	g.table = &nftables.Table{Name: fmt.Sprintf("dobbyvpn_dns_%d_%d", os.Getpid(), resolverQueries.Add(1)), Family: nftables.TableFamilyINet}
	err = g.operation(ctx, func() error {
		if err := createLinuxOwnerTable(g.socket, g.table); err != nil {
			return err
		}
		g.bootstrap = g.conn.AddChain(&nftables.Chain{Table: g.table, Name: "resolve", Type: nftables.ChainTypeRoute, Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityMangle})
		for _, address := range local {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil {
				return err
			}
			for _, server := range servers {
				ip := server.Addr().WithZone("").Unmap()
				if ip.BitLen() != prefix.Addr().BitLen() {
					continue
				}
				for _, endpoint := range []struct {
					protocol byte
					port     uint16
				}{{unix.IPPROTO_TCP, server.Port()}, {unix.IPPROTO_UDP, server.Port()}, {unix.IPPROTO_TCP, 853}} {
					matches := []expr.Any{&expr.Meta{Key: expr.MetaKeySKUID, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: native32(uint32(uid))}}
					matches = append(matches, matchIP(prefix.Addr(), true)...)
					matches = append(matches, matchIP(ip, false)...)
					matches = append(matches, matchProtocol(endpoint.protocol)...)
					matches = append(matches, matchPort(2, endpoint.port)...)
					g.markTraffic(matches)
				}
			}
		}
		return g.conn.Flush()
	})
	if err != nil {
		return nil, errors.Join(err, g.conn.CloseLasting())
	}
	return g.conn.CloseLasting, nil
}

// createLinuxOwnerTable uses the documented NFT_TABLE_F_OWNER kernel flag.
// The upstream released table constructor does not expose that flag, so this
// native table-creation request precedes normal upstream chain/rule operations.
func createLinuxOwnerTable(socket *nl.Conn, table *nftables.Table) error {
	attributes, err := nl.MarshalAttributes([]nl.Attribute{
		{Type: unix.NFTA_TABLE_NAME, Data: append([]byte(table.Name), 0)},
		{Type: unix.NFTA_TABLE_FLAGS, Data: binary.BigEndian.AppendUint32(nil, 2)}, // NFT_TABLE_F_OWNER
	})
	if err != nil {
		return err
	}
	batch := []nl.Message{
		{Header: nl.Header{Type: unix.NFNL_MSG_BATCH_BEGIN, Flags: nl.Request}, Data: []byte{unix.NFPROTO_UNSPEC, unix.NFNETLINK_V0, 0, unix.NFNL_SUBSYS_NFTABLES}},
		{Header: nl.Header{Type: (unix.NFNL_SUBSYS_NFTABLES << 8) | unix.NFT_MSG_NEWTABLE, Flags: nl.Request | nl.Acknowledge | nl.Create | nl.Excl}, Data: append([]byte{byte(table.Family), unix.NFNETLINK_V0, 0, 0}, attributes...)},
		{Header: nl.Header{Type: unix.NFNL_MSG_BATCH_END, Flags: nl.Request}, Data: []byte{unix.NFPROTO_UNSPEC, unix.NFNETLINK_V0, 0, unix.NFNL_SUBSYS_NFTABLES}},
	}
	sent, err := socket.SendMessages(batch)
	if err != nil {
		return err
	}
	replies, err := socket.Receive()
	if err != nil {
		return err
	}
	return nl.Validate(sent[1], replies)
}
