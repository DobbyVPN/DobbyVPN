//go:build linux && !android

package routing

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"os"
	"os/user"
	"strconv"
	"sync"
	"time"

	"core/log"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	nl "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// LinuxOutputGuard owns a netlink-owner table. The kernel removes it if its
// socket/process dies. Its lifetime is the VPN network, never a protocol.
type LinuxOutputGuard struct {
	mu                sync.Mutex
	conn              *nftables.Conn
	socket            *nl.Conn
	table             *nftables.Table
	bootstrap         *nftables.Chain
	mark              uint32
	resolverUID       uint32
	dhcpUIDs          []uint32
	bootstrapIdentity string
	created, closed   bool
}

func (p *Plan) AcquireLinuxOutputGuard(ctx context.Context, tunName string, mark int) (*LinuxOutputGuard, error) {
	if mark <= 0 || uint64(mark) > math.MaxUint32 {
		return nil, errors.New("output guard requires an owned socket mark")
	}
	account, err := user.Lookup("systemd-resolve")
	if err != nil {
		return nil, fmt.Errorf("identify underlying resolver service: %w", err)
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, err
	}
	g := &LinuxOutputGuard{mark: uint32(mark), resolverUID: uint32(uid), dhcpUIDs: []uint32{0}}
	// NetworkManager/dhclient use root; networkd drops to its dedicated account.
	// Ports alone are insufficient: unprivileged applications can bind low
	// ports on systems configured with ip_unprivileged_port_start=0.
	if dhcpAccount, lookupErr := user.Lookup("systemd-network"); lookupErr == nil {
		dhcpUID, parseErr := strconv.ParseUint(dhcpAccount.Uid, 10, 32)
		if parseErr != nil {
			return nil, fmt.Errorf("identify DHCP service: %w", parseErr)
		}
		if dhcpUID != 0 {
			g.dhcpUIDs = append(g.dhcpUIDs, uint32(dhcpUID))
		}
	} else {
		var absent user.UnknownUserError
		if !errors.As(lookupErr, &absent) {
			return nil, fmt.Errorf("identify DHCP service: %w", lookupErr)
		}
	}
	// Register before any native operation: an interrupted ACK can still leave
	// a committed kernel table owned by this socket.
	if _, acquireErr := p.Acquire("owned IPv4/IPv6 output guard", func() error { return nil }, g.Close); acquireErr != nil {
		return nil, acquireErr
	}
	g.conn, err = nftables.New(nftables.AsLasting(), nftables.WithSockOptions(func(socket *nl.Conn) error { g.socket = socket; return nil }))
	if err != nil {
		return g, err
	}
	g.table = &nftables.Table{Name: fmt.Sprintf("dobbyvpn_%d", os.Getpid()), Family: nftables.TableFamilyINet}
	err = g.operation(ctx, func() error {
		if createErr := createLinuxOwnerTable(g.socket, g.table); createErr != nil {
			return createErr
		}
		g.created = true
		priority := nftables.ChainPriorityMangle
		g.bootstrap = g.conn.AddChain(&nftables.Chain{Table: g.table, Name: "bootstrap", Type: nftables.ChainTypeRoute, Hooknum: nftables.ChainHookOutput, Priority: priority})
		policy := nftables.ChainPolicyDrop
		output := g.conn.AddChain(&nftables.Chain{Table: g.table, Name: "protect", Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityFilter, Policy: &policy})
		for _, name := range []string{"lo", tunName} {
			data := make([]byte, unix.IFNAMSIZ)
			copy(data, name)
			g.add(output, []expr.Any{&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: data}}, expr.VerdictAccept)
		}
		g.add(output, []expr.Any{&expr.Meta{Key: expr.MetaKeyMARK, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: native32(g.mark)}}, expr.VerdictAccept)
		// No established-flow exemption: a pre-existing application connection
		// cannot escape merely because its transport used to work.
		g.add(output, []expr.Any{&expr.Counter{}}, expr.VerdictDrop)
		return g.conn.Flush()
	})
	return g, err
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

func native32(value uint32) []byte { return binary.NativeEndian.AppendUint32(nil, value) }
func (g *LinuxOutputGuard) add(chain *nftables.Chain, matches []expr.Any, verdict expr.VerdictKind) {
	g.conn.AddRule(&nftables.Rule{Table: g.table, Chain: chain, Exprs: append(matches, &expr.Verdict{Kind: verdict})})
}
func (g *LinuxOutputGuard) markTraffic(matches []expr.Any) {
	matches = append(matches, &expr.Immediate{Register: 1, Data: native32(g.mark)}, &expr.Meta{Key: expr.MetaKeyMARK, SourceRegister: true, Register: 1})
	g.add(g.bootstrap, matches, expr.VerdictAccept)
}
func matchProtocol(protocol byte) []expr.Any {
	return []expr.Any{&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{protocol}}}
}
func matchPort(offset uint32, port uint16) []expr.Any {
	return []expr.Any{&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: offset, Len: 2}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binary.BigEndian.AppendUint16(nil, port)}}
}
func matchIP(ip netip.Addr) []expr.Any {
	const (
		ipv4AddressLength uint32 = 4
		ipv6AddressLength uint32 = 16
	)
	family, offset, length := byte(unix.NFPROTO_IPV6), uint32(24), ipv6AddressLength
	if ip.Is4() {
		family, offset, length = unix.NFPROTO_IPV4, 16, ipv4AddressLength
	}
	return []expr.Any{&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{family}}, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: length}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip.AsSlice()}}
}

// UpdateBootstrap atomically replaces exceptions for the captured link's DNS.
// Application bypass remains confined to marked sockets created by the owned
// packet router; independent applications receive no destination-wide escape.
func (g *LinuxOutputGuard) UpdateBootstrap(ctx context.Context, identity string, servers []netip.Addr) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return errors.New("output guard is closed")
	}
	if g.bootstrapIdentity == identity {
		return nil
	}
	for _, server := range servers {
		if !server.IsValid() || server.IsLoopback() || server.IsUnspecified() {
			return fmt.Errorf("invalid physical DNS server %s", server)
		}
	}
	err := g.operation(ctx, func() error {
		g.conn.FlushChain(g.bootstrap)
		for _, server := range servers {
			for _, protocol := range []byte{unix.IPPROTO_TCP, unix.IPPROTO_UDP} {
				for _, port := range []uint16{53, 853} {
					if protocol == unix.IPPROTO_UDP && port == 853 {
						continue
					}
					matches := make([]expr.Any, 0, 10)
					matches = append(matches, &expr.Meta{Key: expr.MetaKeySKUID, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: native32(g.resolverUID)})
					matches = append(matches, matchIP(server)...)
					matches = append(matches, matchProtocol(protocol)...)
					matches = append(matches, matchPort(2, port)...)
					g.markTraffic(matches)
				}
			}
		}
		// DHCP clients and IPv6 neighbour discovery must maintain the physical
		// link while all ordinary application packets remain captured/blocked.
		for _, client := range []struct {
			family        byte
			local, remote uint16
		}{{unix.NFPROTO_IPV4, 68, 67}, {unix.NFPROTO_IPV6, 546, 547}} {
			for _, uid := range g.dhcpUIDs {
				matches := make([]expr.Any, 0, 10)
				matches = append(matches,
					&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{client.family}},
					&expr.Meta{Key: expr.MetaKeySKUID, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: native32(uid)},
				)
				matches = append(matches, matchProtocol(unix.IPPROTO_UDP)...)
				matches = append(matches, matchPort(0, client.local)...)
				matches = append(matches, matchPort(2, client.remote)...)
				g.markTraffic(matches)
			}
		}
		for _, kind := range []byte{133, 135, 136} {
			matches := matchProtocol(unix.IPPROTO_ICMPV6)
			matches = append(matches, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{kind}},
				&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 7, Len: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{255}})
			g.markTraffic(matches)
		}
		return g.conn.Flush()
	})
	if err == nil {
		g.bootstrapIdentity = identity
		log.Infof(Category, "output guard physical resolver updated network=%s servers=%v", identity, servers)
	}
	return err
}

func (g *LinuxOutputGuard) operation(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if err := g.socket.SetDeadline(deadline); err != nil {
		return err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = g.socket.SetDeadline(time.Now()); close(done) })
	err := fn()
	if !stop() {
		<-done
	}
	return errors.Join(err, ctx.Err(), g.socket.SetDeadline(time.Time{}))
}

func (g *LinuxOutputGuard) Close(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	if g.conn != nil && g.created {
		if err := g.operation(ctx, func() error { g.conn.DelTable(g.table); return g.conn.Flush() }); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
		g.created = false
	}
	if g.conn != nil {
		if err := g.conn.CloseLasting(); err != nil {
			return err
		}
	} else if g.socket != nil {
		if err := g.socket.Close(); err != nil {
			return err
		}
	}
	g.closed = true
	return nil
}
