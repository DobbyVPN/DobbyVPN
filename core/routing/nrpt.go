package routing

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// The Windows DNS Client keeps querying every adapter's DNS servers unless the
// Name Resolution Policy Table (NRPT) says otherwise. While connected, the
// service adds one rule for the root namespace "." that sends every query to
// the tunnel DNS servers, so the DNS Client stops waiting for the physical
// adapter's servers, which the network guard blocks.
//
// The rule is a local NRPT rule: one registry key under DnsPolicyConfig, the
// same storage Add-DnsClientNrptRule writes and Get-DnsClientNrptRule reads.
// It survives a crash, so it is ours only if it has both the fixed key name
// and the fixed comment below; any other rule is never changed or removed.
const (
	OwnedNRPTRuleKey  = "{6d0b1b5e-7c3a-4f52-9e1d-2a8b4c6f0d17}"
	OwnedNRPTComment  = "DobbyVPN tunnel DNS. Added while connected and removed by the DobbyVPN service."
	ownedNRPTDisplay  = "DobbyVPN"
	nrptRuleVersion   = 2
	nrptGenericServer = 0x8 // ConfigOptions: use GenericDNSServers for the namespace.
)

// ErrForeignNRPTRule means the owned key holds a rule this service did not
// write. It is left untouched.
var ErrForeignNRPTRule = errors.New("NRPT rule at the DobbyVPN key is not owned by DobbyVPN; left untouched")

// NRPTRule holds the registry values of one local NRPT rule.
type NRPTRule struct {
	Version       uint32
	Names         []string
	Servers       string
	ConfigOptions uint32
	Comment       string
	DisplayName   string
}

func (r NRPTRule) owned() bool { return r.Comment == OwnedNRPTComment }

// nrptStore is the registry seen through one rule key, plus the call that
// makes the DNS Client use the new table.
type nrptStore interface {
	Read(key string) (rule NRPTRule, exists bool, err error)
	Write(key string, rule NRPTRule) error
	Delete(key string) error
	Reload() error
}

func ownedNRPTRule(servers []netip.Addr) (NRPTRule, error) {
	if len(servers) == 0 {
		return NRPTRule{}, errors.New("NRPT rule needs at least one tunnel DNS server")
	}
	text := make([]string, 0, len(servers))
	for _, server := range servers {
		if !server.IsValid() || server.Zone() != "" {
			return NRPTRule{}, fmt.Errorf("invalid tunnel DNS server %q for NRPT", server)
		}
		text = append(text, server.String())
	}
	return NRPTRule{
		Version:       nrptRuleVersion,
		Names:         []string{"."},
		Servers:       strings.Join(text, "; "),
		ConfigOptions: nrptGenericServer,
		Comment:       OwnedNRPTComment,
		DisplayName:   ownedNRPTDisplay,
	}, nil
}

func sameNRPTRule(a, b NRPTRule) bool {
	return a.Version == b.Version && slices.Equal(a.Names, b.Names) && a.Servers == b.Servers &&
		a.ConfigOptions == b.ConfigOptions && a.Comment == b.Comment && a.DisplayName == b.DisplayName
}

// installOwnedNRPT writes the owned rule, or replaces an owned rule left by an
// earlier session, and checks that the registry holds exactly that rule.
func installOwnedNRPT(store nrptStore, servers []netip.Addr) error {
	rule, err := ownedNRPTRule(servers)
	if err != nil {
		return err
	}
	current, exists, err := store.Read(OwnedNRPTRuleKey)
	if err != nil {
		return fmt.Errorf("read NRPT rule: %w", err)
	}
	if exists && !current.owned() {
		return ErrForeignNRPTRule
	}
	if err := store.Write(OwnedNRPTRuleKey, rule); err != nil {
		return fmt.Errorf("write NRPT rule: %w", err)
	}
	written, exists, err := store.Read(OwnedNRPTRuleKey)
	if err != nil {
		return fmt.Errorf("verify NRPT rule: %w", err)
	}
	if !exists || !sameNRPTRule(written, rule) {
		return errors.New("NRPT rule differs from the rule just written")
	}
	if err := store.Reload(); err != nil {
		return fmt.Errorf("reload DNS Client after adding NRPT rule: %w", err)
	}
	return nil
}

// removeOwnedNRPT deletes the owned rule if it is present. It reports whether
// a rule was removed. A foreign rule at the key is kept and reported.
func removeOwnedNRPT(store nrptStore) (bool, error) {
	current, exists, err := store.Read(OwnedNRPTRuleKey)
	if err != nil {
		return false, fmt.Errorf("read NRPT rule: %w", err)
	}
	if !exists {
		return false, nil
	}
	if !current.owned() {
		return false, ErrForeignNRPTRule
	}
	deleteErr := store.Delete(OwnedNRPTRuleKey)
	if _, still, err := store.Read(OwnedNRPTRuleKey); err != nil {
		return false, errors.Join(deleteErr, fmt.Errorf("verify NRPT rule removal: %w", err))
	} else if still {
		return false, errors.Join(deleteErr, errors.New("NRPT rule remained after deletion"))
	}
	if err := store.Reload(); err != nil {
		return true, fmt.Errorf("reload DNS Client after removing NRPT rule: %w", err)
	}
	return true, nil
}
