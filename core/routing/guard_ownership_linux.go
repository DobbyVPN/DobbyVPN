//go:build linux && !android

package routing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	nl "github.com/mdlayher/netlink"

	"github.com/google/nftables"
	"github.com/google/nftables/userdata"
)

const (
	linuxBootstrapChain = "bootstrap"
	linuxGuardResolver  = "DobbyVPN physical resolver"
	linuxGuardTable     = "dobbyvpn_guard"
	linuxGuardOwner     = "DobbyVPN output guard schema 1"
)

// A shared name alone grants no authority over a kernel object. Check the
// marker and the base-chain/set schema before adopting or deleting the table.
func ownedLinuxGuardTable(conn *nftables.Conn) (*nftables.Table, error) {
	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		if table.Name != linuxGuardTable {
			continue
		}
		if err := verifyLinuxGuardSchema(conn, table); err != nil {
			return nil, fmt.Errorf("refusing to change inet table %s: %w", table.Name, err)
		}
		return table, nil
	}
	return nil, nil
}

func verifyLinuxGuardSchema(conn *nftables.Conn, table *nftables.Table) error {
	if table.Flags != 0 {
		return fmt.Errorf("unexpected table flags %#x", table.Flags)
	}
	chains, err := conn.ListChainsOfTableFamily(table.Family)
	if err != nil {
		return err
	}
	for _, chain := range chains {
		if chain.Table.Name == table.Name && chain.Name != "protect" && chain.Name != linuxBootstrapChain {
			return fmt.Errorf("unexpected chain %s", chain.Name)
		}
	}
	protect, err := conn.ListChain(table, "protect")
	if err != nil {
		return err
	}
	bootstrap, err := conn.ListChain(table, linuxBootstrapChain)
	if err != nil {
		return err
	}
	if schemaErr := validateLinuxBaseChains(protect, bootstrap); schemaErr != nil {
		return schemaErr
	}
	if setErr := verifyLinuxGuardSets(conn, table); setErr != nil {
		return setErr
	}
	rules, err := conn.GetRules(table, protect)
	if err != nil {
		return err
	}
	marker := userdata.AppendString(nil, userdata.TypeComment, linuxGuardOwner)
	if len(rules) == 0 || !bytes.Equal(rules[len(rules)-1].UserData, marker) {
		return fmt.Errorf("missing DobbyVPN ownership marker")
	}
	return nil
}

func validateLinuxBaseChains(protect, bootstrap *nftables.Chain) error {
	if protect.Type != nftables.ChainTypeFilter || protect.Hooknum == nil || *protect.Hooknum != *nftables.ChainHookOutput ||
		protect.Policy == nil || *protect.Policy != nftables.ChainPolicyDrop || protect.Priority == nil || *protect.Priority != *nftables.ChainPriorityFilter {
		return fmt.Errorf("unexpected protect chain schema")
	}
	if bootstrap.Type != nftables.ChainTypeRoute || bootstrap.Hooknum == nil || *bootstrap.Hooknum != *nftables.ChainHookOutput ||
		bootstrap.Priority == nil || *bootstrap.Priority != *nftables.ChainPriorityMangle {
		return fmt.Errorf("unexpected bootstrap chain schema")
	}
	return nil
}

func verifyLinuxGuardSets(conn *nftables.Conn, table *nftables.Table) error {
	sets, err := conn.GetSets(table)
	if err != nil {
		return err
	}
	known := map[string]nftables.SetDatatype{"excluded4": nftables.TypeIPAddr, "excluded6": nftables.TypeIP6Addr}
	for _, set := range sets {
		expected, ok := known[set.Name]
		if !ok || !set.Interval || set.KeyType.GetNFTMagic() != expected.GetNFTMagic() {
			return fmt.Errorf("unexpected set %s", set.Name)
		}
		delete(known, set.Name)
	}
	if len(known) != 0 {
		return fmt.Errorf("missing address sets")
	}
	return nil
}

func removeOwnedLinuxGuard(conn *nftables.Conn) error {
	table, err := ownedLinuxGuardTable(conn)
	if err != nil || table == nil {
		return err
	}
	conn.DelTable(table)
	return conn.Flush()
}

var linuxGuardLifetime struct {
	sync.Mutex
	active *LinuxOutputGuard
	retain bool
}

type LinuxServiceProtection struct{}

func withLinuxGuardConn(ctx context.Context, action func(*nftables.Conn) error) (err error) {
	var guard LinuxOutputGuard
	guard.conn, err = nftables.New(nftables.AsLasting(), nftables.WithSockOptions(func(socket *nl.Conn) error {
		guard.socket = socket
		return nil
	}))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, guard.conn.CloseLasting()) }()
	return guard.operation(ctx, func() error { return action(guard.conn) })
}

// Recover leaves the base drop policy in place and revokes the previous
// attempt's exceptions before the service can load a subscription or add routes.
func (LinuxServiceProtection) Recover(ctx context.Context) (bool, error) {
	linuxGuardLifetime.Lock()
	defer linuxGuardLifetime.Unlock()
	var retained bool
	err := withLinuxGuardConn(ctx, func(conn *nftables.Conn) error {
		table, err := ownedLinuxGuardTable(conn)
		if err != nil || table == nil {
			return err
		}
		retained, linuxGuardLifetime.retain = true, true
		return clearLinuxGuardAllowances(conn, table)
	})
	return retained, err
}

func (LinuxServiceProtection) SetEnabled(ctx context.Context, enabled bool) error {
	linuxGuardLifetime.Lock()
	defer linuxGuardLifetime.Unlock()
	linuxGuardLifetime.retain = enabled
	if enabled || linuxGuardLifetime.active != nil {
		return nil
	}
	return withLinuxGuardConn(ctx, removeOwnedLinuxGuard)
}

func resetLinuxGuardAllowances(conn *nftables.Conn) error {
	table, err := ownedLinuxGuardTable(conn)
	if err != nil || table == nil {
		return err
	}
	return clearLinuxGuardAllowances(conn, table)
}

func clearLinuxGuardAllowances(conn *nftables.Conn, table *nftables.Table) error {
	sets, err := conn.GetSets(table)
	if err != nil {
		return err
	}
	bootstrap := &nftables.Chain{Table: table, Name: linuxBootstrapChain}
	rules, err := conn.GetRules(table, bootstrap)
	if err != nil {
		return err
	}
	for _, set := range sets {
		conn.FlushSet(set)
	}
	marker := userdata.AppendString(nil, userdata.TypeComment, linuxGuardResolver)
	for _, rule := range rules {
		if bytes.Equal(rule.UserData, marker) {
			if err := conn.DelRule(rule); err != nil {
				return err
			}
		}
	}
	return conn.Flush()
}
