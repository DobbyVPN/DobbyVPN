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
// It survives a crash. Only the fixed key below is ever changed, and only if
// it is ours: it carries the fixed comment, or it has no values at all (an
// interrupted write or removal; such a key is not an active rule).
//
// Every change, including deleting the key, goes through one open handle, so
// the checked key is the key that is changed. Writing puts the comment first,
// before any value that can describe a rule, and ConfigOptions last; removal
// deletes values not in that list first, then the list in reverse, and the
// comment last. An interruption at any step therefore leaves a key that the
// next removal still recognizes as ours.
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

// nrptValue is one registry value: uint32 (DWORD), string or []string.
type nrptValue = any

// nrptValues are the values of one rule key, by registry value name.
type nrptValues map[string]nrptValue

// nrptKey is an open handle to one rule key. A missing value is simply absent
// from Values; any other read failure is an error.
type nrptKey interface {
	Values() (nrptValues, error)
	Set(name string, value nrptValue) error
	DeleteValue(name string) error
	// DeleteIfEmpty deletes the key this handle refers to, only if it has no
	// values and no subkeys. A key that was replaced under the same name
	// after this handle was opened is a different key and is not deleted.
	DeleteIfEmpty() error
	Close() error
}

// nrptStore opens rule keys under DnsPolicyConfig and makes the DNS Client
// use the changed table.
type nrptStore interface {
	// Open returns nil, nil when the key is absent and create is false.
	Open(key string, create bool) (nrptKey, error)
	Reload() error
}

// nrptWriteOrder puts the ownership marker first and the value that makes the
// rule take effect last. Removal uses the reverse order.
var nrptWriteOrder = []string{"Comment", "DisplayName", "Version", "IPSECCARestriction", "Name", "GenericDNSServers", "ConfigOptions"}

func ownedNRPTValues(servers []netip.Addr) (nrptValues, error) {
	if len(servers) == 0 {
		return nil, errors.New("NRPT rule needs at least one tunnel DNS server")
	}
	text := make([]string, 0, len(servers))
	for _, server := range servers {
		if !server.IsValid() || server.Zone() != "" {
			return nil, fmt.Errorf("invalid tunnel DNS server %q for NRPT", server)
		}
		text = append(text, server.String())
	}
	return nrptValues{
		"Comment":            OwnedNRPTComment,
		"DisplayName":        ownedNRPTDisplay,
		"Version":            uint32(nrptRuleVersion),
		"IPSECCARestriction": "",
		"Name":               []string{"."},
		"GenericDNSServers":  strings.Join(text, "; "),
		"ConfigOptions":      uint32(nrptGenericServer),
	}, nil
}

// ownedNRPTKey reports whether values belong to a key this service may change.
func ownedNRPTKey(values nrptValues) bool {
	if len(values) == 0 {
		return true // interrupted write or removal; not an active rule
	}
	comment, ok := values["Comment"].(string)
	return ok && comment == OwnedNRPTComment
}

func sameNRPTValues(a, b nrptValues) bool {
	if len(a) != len(b) {
		return false
	}
	for name, want := range a {
		switch want := want.(type) {
		case []string:
			got, ok := b[name].([]string)
			if !ok || !slices.Equal(got, want) {
				return false
			}
		default:
			if b[name] != want {
				return false
			}
		}
	}
	return true
}

// installOwnedNRPT writes the owned rule, or rewrites an owned rule left by an
// earlier session, and checks that the key holds exactly that rule.
func installOwnedNRPT(store nrptStore, servers []netip.Addr) error {
	rule, err := ownedNRPTValues(servers)
	if err != nil {
		return err
	}
	key, err := store.Open(OwnedNRPTRuleKey, true)
	if err != nil {
		return fmt.Errorf("open NRPT rule: %w", err)
	}
	writeErr := func() error {
		current, err := key.Values()
		if err != nil {
			return fmt.Errorf("read NRPT rule: %w", err)
		}
		if !ownedNRPTKey(current) {
			return ErrForeignNRPTRule
		}
		for name := range current {
			if _, keep := rule[name]; !keep {
				if err := key.DeleteValue(name); err != nil {
					return fmt.Errorf("clear NRPT value %s: %w", name, err)
				}
			}
		}
		for _, name := range nrptWriteOrder {
			if err := key.Set(name, rule[name]); err != nil {
				return fmt.Errorf("write NRPT value %s: %w", name, err)
			}
		}
		return nil
	}()
	if err := errors.Join(writeErr, key.Close()); err != nil {
		return err
	}
	// A fresh handle shows what the DNS Client sees under the key name.
	written, err := readNRPTKey(store)
	if err != nil {
		return fmt.Errorf("verify NRPT rule: %w", err)
	}
	if !sameNRPTValues(rule, written) {
		return errors.New("NRPT rule at the DobbyVPN key differs from the rule just written")
	}
	if err := store.Reload(); err != nil {
		return fmt.Errorf("reload DNS Client after adding NRPT rule: %w", err)
	}
	return nil
}

func readNRPTKey(store nrptStore) (nrptValues, error) {
	key, err := store.Open(OwnedNRPTRuleKey, false)
	if err != nil || key == nil {
		return nil, err
	}
	values, err := key.Values()
	return values, errors.Join(err, key.Close())
}

// removeOwnedNRPT removes the owned rule if it is present. It reports whether
// a key was removed. A foreign rule at the key is kept and reported.
func removeOwnedNRPT(store nrptStore) (bool, error) {
	key, err := store.Open(OwnedNRPTRuleKey, false)
	if err != nil {
		return false, fmt.Errorf("open NRPT rule: %w", err)
	}
	if key == nil {
		return false, nil
	}
	clearErr := func() error {
		current, err := key.Values()
		if err != nil {
			return fmt.Errorf("read NRPT rule: %w", err)
		}
		if !ownedNRPTKey(current) {
			return ErrForeignNRPTRule
		}
		// The comment goes last, so the key stays recognizable as ours.
		for name := range current {
			if !slices.Contains(nrptWriteOrder, name) {
				if err := key.DeleteValue(name); err != nil {
					return fmt.Errorf("delete NRPT value %s: %w", name, err)
				}
			}
		}
		for _, name := range slices.Backward(nrptWriteOrder) {
			if _, present := current[name]; present {
				if err := key.DeleteValue(name); err != nil {
					return fmt.Errorf("delete NRPT value %s: %w", name, err)
				}
			}
		}
		return key.DeleteIfEmpty()
	}()
	if err := errors.Join(clearErr, key.Close()); err != nil {
		return false, err
	}
	if left, err := store.Open(OwnedNRPTRuleKey, false); err != nil {
		return false, fmt.Errorf("verify NRPT rule removal: %w", err)
	} else if left != nil {
		_ = left.Close()
		return false, errors.New("a key is at the NRPT rule name after removal")
	}
	if err := store.Reload(); err != nil {
		return true, fmt.Errorf("reload DNS Client after removing NRPT rule: %w", err)
	}
	return true, nil
}
