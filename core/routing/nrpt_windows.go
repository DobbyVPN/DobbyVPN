//go:build windows

package routing

import (
	"errors"
	"fmt"
	"net/netip"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const (
	nrptLocalPath = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
	// Group Policy rules live here; while any exist, Windows ignores local rules.
	nrptPolicyPath = `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`
)

var procDnsFlushResolverCache = windows.NewLazySystemDLL("dnsapi.dll").NewProc("DnsFlushResolverCache")

type registryNRPTStore struct{}

func (registryNRPTStore) Read(key string) (NRPTRule, bool, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, nrptLocalPath+`\`+key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return NRPTRule{}, false, nil
	}
	if err != nil {
		return NRPTRule{}, false, err
	}
	defer k.Close()
	var rule NRPTRule
	// A value that is missing or has another type reads as empty, so the rule
	// is then neither owned nor equal to the one written.
	version, _, _ := k.GetIntegerValue("Version")
	options, _, _ := k.GetIntegerValue("ConfigOptions")
	rule.Version, rule.ConfigOptions = uint32(version), uint32(options)
	rule.Names, _, _ = k.GetStringsValue("Name")
	rule.Servers, _, _ = k.GetStringValue("GenericDNSServers")
	rule.Comment, _, _ = k.GetStringValue("Comment")
	rule.DisplayName, _, _ = k.GetStringValue("DisplayName")
	return rule, true, nil
}

func (registryNRPTStore) Write(key string, rule NRPTRule) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, nrptLocalPath+`\`+key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return errors.Join(
		k.SetDWordValue("Version", rule.Version),
		k.SetStringsValue("Name", rule.Names),
		k.SetStringValue("GenericDNSServers", rule.Servers),
		k.SetDWordValue("ConfigOptions", rule.ConfigOptions),
		k.SetStringValue("IPSECCARestriction", ""),
		k.SetStringValue("Comment", rule.Comment),
		k.SetStringValue("DisplayName", rule.DisplayName),
	)
}

func (registryNRPTStore) Delete(key string) error {
	parent, err := registry.OpenKey(registry.LOCAL_MACHINE, nrptLocalPath, registry.ENUMERATE_SUB_KEYS|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := registry.DeleteKey(parent, key); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// Reload drops answers cached under the previous table. The DNS Client reads
// the changed DnsPolicyConfig key itself.
func (registryNRPTStore) Reload() error {
	if err := procDnsFlushResolverCache.Find(); err != nil {
		return err
	}
	if ok, _, callErr := procDnsFlushResolverCache.Call(); ok == 0 {
		return fmt.Errorf("DnsFlushResolverCache: %w", callErr)
	}
	return nil
}

var windowsNRPTStore nrptStore = registryNRPTStore{}

// InstallWindowsNRPT adds the owned rule that sends all names to servers.
func InstallWindowsNRPT(servers []netip.Addr) error {
	return installOwnedNRPT(windowsNRPTStore, servers)
}

// RemoveWindowsNRPT removes the owned rule. It reports whether one was present.
func RemoveWindowsNRPT() (bool, error) {
	return removeOwnedNRPT(windowsNRPTStore)
}

func countSubKeys(path string) (int, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer k.Close()
	info, err := k.Stat()
	if err != nil {
		return 0, err
	}
	return int(info.SubKeyCount), nil
}

// addNRPTState records the NRPT table and interface metrics in fields.
func addNRPTState(fields map[string]any, tunnel winipcfg.LUID) {
	if rule, exists, err := windowsNRPTStore.Read(OwnedNRPTRuleKey); err != nil {
		fields["nrpt_owned_error"] = err.Error()
	} else {
		fields["nrpt_owned_present"] = exists && rule.owned()
	}
	if count, err := countSubKeys(nrptLocalPath); err != nil {
		fields["nrpt_local_rules_error"] = err.Error()
	} else {
		fields["nrpt_local_rules"] = count
	}
	if count, err := countSubKeys(nrptPolicyPath); err != nil {
		fields["nrpt_policy_rules_error"] = err.Error()
	} else {
		fields["nrpt_policy_rules"] = count // non-zero: local rules are ignored
	}
	if row, err := tunnel.IPInterface(windows.AF_INET); err == nil {
		fields["tunnel_metric_ipv4"] = row.Metric
	}
	if index, _, err := WindowsPhysicalInterfaces(); err == nil && index != 0 {
		if luid, err := winipcfg.LUIDFromIndex(uint32(index)); err == nil {
			if row, err := luid.IPInterface(windows.AF_INET); err == nil {
				fields["physical_if_index_ipv4"] = index
				fields["physical_metric_ipv4"] = row.Metric
			}
		}
	}
}
