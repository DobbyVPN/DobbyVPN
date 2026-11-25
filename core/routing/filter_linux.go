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
	"strings"
	"sync"
	"time"

	"core/log"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	nl "github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// LinuxOutputGuard manages the persistent inet table. Closing the socket or
// losing the userspace service leaves the last committed protection in force.
type LinuxOutputGuard struct {
	mu        sync.Mutex
	conn      *nftables.Conn
	socket    *nl.Conn
	table     *nftables.Table
	bootstrap *nftables.Chain
	mark      uint32

	dhcpUIDs []uint32

	created, closed bool
}

func (p *Plan) AcquireLinuxOutputGuard(ctx context.Context, tunName string, mark int) (*LinuxOutputGuard, error) {
	linuxGuardLifetime.Lock()
	defer linuxGuardLifetime.Unlock()
	if linuxGuardLifetime.active != nil {
		return nil, errors.New("another network owns the Linux output guard")
	}
	if mark <= 0 || uint64(mark) > math.MaxUint32 {
		return nil, errors.New("output guard requires an owned socket mark")
	}
	managementPeers, err := linuxManagementPeers(os.Getenv("DOBBYVPN_LINUX_MANAGEMENT_PEERS"))
	if err != nil {
		return nil, err
	}
	g := &LinuxOutputGuard{mark: uint32(mark), dhcpUIDs: []uint32{0}}
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
	// a committed kernel table.
	if _, acquireErr := p.Acquire("owned IPv4/IPv6 output guard", func() error { return nil }, g.Close); acquireErr != nil {
		return nil, acquireErr
	}
	g.conn, err = nftables.New(nftables.AsLasting(), nftables.WithSockOptions(func(socket *nl.Conn) error { g.socket = socket; return nil }))
	if err != nil {
		return g, err
	}
	g.table = &nftables.Table{Name: linuxGuardTable, Family: nftables.TableFamilyINet}
	err = g.operation(ctx, func() error {
		existing, inspectErr := ownedLinuxGuardTable(g.conn)
		if inspectErr != nil {
			return inspectErr
		}
		if existing != nil {
			// Both operations belong to one nftables batch. The kernel sees
			// the previous policy or the complete replacement, including if
			// creating any new chain, set or rule fails.
			g.conn.DelTable(existing)
		}
		g.conn.CreateTable(g.table)
		// Released clients validate these two sets before recovering a retained
		// table on downgrade. Keep the schema but never populate or match them.
		for _, set := range []*nftables.Set{
			{Table: g.table, Name: "excluded4", KeyType: nftables.TypeIPAddr, Interval: true},
			{Table: g.table, Name: "excluded6", KeyType: nftables.TypeIP6Addr, Interval: true},
		} {
			if err := g.conn.AddSet(set, nil); err != nil {
				return err
			}
		}
		priority := nftables.ChainPriorityMangle
		g.bootstrap = g.conn.AddChain(&nftables.Chain{Table: g.table, Name: linuxBootstrapChain, Type: nftables.ChainTypeRoute, Hooknum: nftables.ChainHookOutput, Priority: priority})
		g.addLinkMaintenanceRules()
		for _, peer := range managementPeers {
			// Only replies to peer-initiated TCP connections qualify. A
			// local application dialing the same peer still uses the VPN.
			matches := append(matchProtocol(unix.IPPROTO_TCP), matchIP(peer, false)...)
			matches = append(matches, &expr.Ct{Key: expr.CtKeyDIRECTION, Register: 1},
				&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{1}}) // IP_CT_DIR_REPLY
			g.markTraffic(matches)
		}
		policy := nftables.ChainPolicyDrop
		output := g.conn.AddChain(&nftables.Chain{Table: g.table, Name: "protect", Type: nftables.ChainTypeFilter, Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityFilter, Policy: &policy})
		interfaces := []string{"lo"}
		if tunName != "" {
			interfaces = append(interfaces, tunName)
		}
		for _, name := range interfaces {
			data := make([]byte, unix.IFNAMSIZ)
			copy(data, name)
			g.add(output, []expr.Any{&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: data}}, expr.VerdictAccept)
		}
		g.add(output, []expr.Any{&expr.Meta{Key: expr.MetaKeyMARK, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: native32(g.mark)}}, expr.VerdictAccept)
		// No established-flow exemption: a pre-existing application connection
		// cannot escape merely because its transport used to work.
		g.conn.AddRule(&nftables.Rule{Table: g.table, Chain: output,
			Exprs:    []expr.Any{&expr.Counter{}, &expr.Verdict{Kind: expr.VerdictDrop}},
			UserData: userdata.AppendString(nil, userdata.TypeComment, linuxGuardOwner),
		})
		if flushErr := g.conn.Flush(); flushErr != nil {
			return flushErr
		}
		g.created = true
		linuxGuardLifetime.active = g
		log.Infof(Category, "persistent output guard active table=%s adopted=%t", g.table.Name, existing != nil)
		if len(managementPeers) != 0 {
			log.Info(Category, "Linux management TCP replies enabled", map[string]any{"peer_count": len(managementPeers)})
		}
		return nil
	})
	return g, err
}

func linuxManagementPeers(value string) ([]netip.Addr, error) {
	fields := strings.Fields(value)
	if len(fields) > 16 {
		return nil, errors.New("DOBBYVPN_LINUX_MANAGEMENT_PEERS accepts at most 16 IP addresses")
	}
	var peers []netip.Addr
	for index, field := range fields {
		peer, err := netip.ParseAddr(field)
		if err != nil || peer.Zone() != "" || !peer.IsGlobalUnicast() {
			return nil, fmt.Errorf("invalid management IP address at position %d", index+1)
		}
		peers = append(peers, peer.Unmap())
	}
	return peers, nil
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
func matchIP(ip netip.Addr, source bool) []expr.Any {
	const (
		ipv4AddressLength uint32 = 4
		ipv6AddressLength uint32 = 16
	)
	family, offset, length := byte(unix.NFPROTO_IPV6), uint32(24), ipv6AddressLength
	if ip.Is4() {
		family, offset, length = unix.NFPROTO_IPV4, 16, ipv4AddressLength
	}
	if source {
		offset -= length
	}
	return []expr.Any{&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{family}}, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: length}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip.AsSlice()}}
}

func (g *LinuxOutputGuard) addLinkMaintenanceRules() {
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
	// ICMPv6 type 143 (MLDv2 report) keeps multicast membership, and so MLD-snooping
	// switches, working. Reports are sent with hop limit 1.
	mld := matchProtocol(unix.IPPROTO_ICMPV6)
	mld = append(mld, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{143}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 7, Len: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{1}})
	g.markTraffic(mld)
	for _, kind := range []byte{133, 135, 136} {
		matches := matchProtocol(unix.IPPROTO_ICMPV6)
		matches = append(matches, &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 0, Len: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{kind}},
			&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 7, Len: 1}, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{255}})
		g.markTraffic(matches)
	}
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
	linuxGuardLifetime.Lock()
	defer linuxGuardLifetime.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return nil
	}
	if g.conn != nil && g.created {
		if err := g.operation(ctx, func() error {
			if linuxGuardLifetime.retain {
				return resetLinuxGuardAllowances(g.conn)
			}
			return removeOwnedLinuxGuard(g.conn)
		}); err != nil {
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
	if linuxGuardLifetime.active == g {
		linuxGuardLifetime.active = nil
	}
	g.closed = true
	return nil
}
