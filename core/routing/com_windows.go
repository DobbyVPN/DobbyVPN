//go:build windows

package routing

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"strings"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func withCOM(progID string, operation func(*ole.IDispatch) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	var status *ole.OleError
	if err != nil && (!errors.As(err, &status) || status.Code() != 1) {
		return fmt.Errorf("initialize COM: %w", err)
	}
	defer ole.CoUninitialize()
	unknown, err := oleutil.CreateObject(progID)
	if err != nil {
		return fmt.Errorf("create %s: %w", progID, err)
	}
	defer unknown.Release()
	object, err := unknown.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer object.Release()
	return operation(object)
}

func comEach(collection *ole.IDispatch, visit func(*ole.IDispatch) error) error {
	value, err := oleutil.GetProperty(collection, "_NewEnum")
	if err != nil {
		return err
	}
	defer value.Clear()
	enumerator, err := value.ToIUnknown().IEnumVARIANT(ole.IID_IEnumVariant)
	if err != nil {
		return err
	}
	defer enumerator.Release()
	for {
		item, count, err := enumerator.Next(1)
		var status *ole.OleError
		if errors.As(err, &status) && status.Code() == 1 && count == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		err = visit(item.ToIDispatch())
		clearErr := item.Clear()
		if err != nil || clearErr != nil {
			return errors.Join(err, clearErr)
		}
	}
}

// SetWindowsDNS uses WMI's per-adapter API, available at the existing Windows
// minimum. No-argument SetDNSServerSearchOrder restores DHCP DNS assignment.
func SetWindowsDNS(luid winipcfg.LUID, static bool, servers []netip.Addr) error {
	guid, err := luid.GUID()
	if err != nil {
		return fmt.Errorf("resolve DNS adapter GUID: %w", err)
	}
	return withCOM("WbemScripting.SWbemLocator", func(locator *ole.IDispatch) error {
		services, err := oleutil.CallMethod(locator, "ConnectServer", ".", `ROOT\CIMV2`)
		if err != nil {
			return err
		}
		defer services.Clear()
		query := fmt.Sprintf("SELECT * FROM Win32_NetworkAdapterConfiguration WHERE SettingID = '%s'", guid.String())
		rows, err := oleutil.CallMethod(services.ToIDispatch(), "ExecQuery", query)
		if err != nil {
			return err
		}
		defer rows.Clear()
		matched := 0
		err = comEach(rows.ToIDispatch(), func(adapter *ole.IDispatch) error {
			matched++
			if matched > 1 {
				return fmt.Errorf("multiple DNS adapters match GUID %s", guid)
			}
			var args []any
			if static {
				addresses := make([]string, len(servers))
				for index, server := range servers {
					addresses[index] = server.String()
				}
				args = []any{addresses}
			}
			result, err := oleutil.CallMethod(adapter, "SetDNSServerSearchOrder", args...)
			if err != nil {
				return fmt.Errorf("WMI SetDNSServerSearchOrder: %w", err)
			}
			defer result.Clear()
			if result.Val != 0 {
				return fmt.Errorf("WMI SetDNSServerSearchOrder returned %d (1 requires reboot)", result.Val)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if matched != 1 {
			return fmt.Errorf("DNS adapter GUID %s not found in WMI", guid)
		}
		return nil
	})
}

const windowsIPv6Range = "0000:0000:0000:0000:0000:0000:0000:0000-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"

func withFirewallRules(operation func(*ole.IDispatch) error) error {
	return withCOM("HNetCfg.FwPolicy2", func(policy *ole.IDispatch) error {
		rules, err := oleutil.GetProperty(policy, "Rules")
		if err != nil {
			return err
		}
		defer rules.Clear()
		return operation(rules.ToIDispatch())
	})
}

func addWindowsIPv6Rule(name string) error {
	return withFirewallRules(func(rules *ole.IDispatch) error {
		if err := comEach(rules, func(rule *ole.IDispatch) error {
			current, err := oleutil.GetProperty(rule, "Name")
			if err != nil {
				return err
			}
			defer current.Clear()
			if current.ToString() == name {
				return fmt.Errorf("firewall rule name %s already exists", name)
			}
			return nil
		}); err != nil {
			return err
		}
		return withCOM("HNetCfg.FWRule", func(rule *ole.IDispatch) error {
			for _, property := range []struct {
				name  string
				value any
			}{
				{"Name", name}, {"Description", "DobbyVPN session-owned IPv6 block"},
				{"Direction", int32(2)}, {"Action", int32(0)}, {"Enabled", true},
				{"Profiles", int32(0x7fffffff)}, {"Protocol", int32(256)}, {"RemoteAddresses", windowsIPv6Range},
			} {
				result, err := oleutil.PutProperty(rule, property.name, property.value)
				if err != nil {
					return fmt.Errorf("set firewall %s: %w", property.name, err)
				}
				if err := result.Clear(); err != nil {
					return err
				}
			}
			result, err := oleutil.CallMethod(rules, "Add", rule)
			if err != nil {
				return fmt.Errorf("add firewall rule %s: %w", name, err)
			}
			return result.Clear()
		})
	})
}

func removeWindowsIPv6Rules(match func(string) bool) error {
	return withFirewallRules(func(rules *ole.IDispatch) error {
		var names []string
		err := comEach(rules, func(rule *ole.IDispatch) error {
			name, err := oleutil.GetProperty(rule, "Name")
			if err != nil {
				return err
			}
			defer name.Clear()
			if !match(name.ToString()) {
				return nil
			}
			// A matching reserved name supplies ownership evidence. Do not remove
			// a rule another actor has changed into a different firewall policy.
			for _, property := range []struct {
				name  string
				value int64
			}{{"Direction", 2}, {"Action", 0}, {"Protocol", 256}} {
				actual, err := oleutil.GetProperty(rule, property.name)
				if err != nil {
					return err
				}
				value := actual.Val
				if err := actual.Clear(); err != nil {
					return err
				}
				if value != property.value {
					return fmt.Errorf("owned firewall rule %s changed %s", name.ToString(), property.name)
				}
			}
			remote, err := oleutil.GetProperty(rule, "RemoteAddresses")
			if err != nil {
				return err
			}
			remoteText := remote.ToString()
			if err := remote.Clear(); err != nil {
				return err
			}
			if !isWindowsIPv6Range(remoteText) {
				return fmt.Errorf("owned firewall rule %s changed remote range: %s", name.ToString(), remoteText)
			}
			names = append(names, name.ToString())
			return nil
		})
		if err != nil {
			return err
		}
		for _, name := range names {
			result, err := oleutil.CallMethod(rules, "Remove", name)
			if err != nil {
				return fmt.Errorf("remove firewall rule %s: %w", name, err)
			}
			if err := result.Clear(); err != nil {
				return err
			}
		}
		return comEach(rules, func(rule *ole.IDispatch) error {
			name, err := oleutil.GetProperty(rule, "Name")
			if err != nil {
				return err
			}
			defer name.Clear()
			if match(name.ToString()) {
				return fmt.Errorf("firewall rule remains after removal: %s", name.ToString())
			}
			return nil
		})
	})
}

func isWindowsIPv6Range(value string) bool {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix == netip.MustParsePrefix("::/0")
	}
	first, last, ok := strings.Cut(value, "-")
	if !ok {
		return false
	}
	start, err := netip.ParseAddr(first)
	if err != nil {
		return false
	}
	end, err := netip.ParseAddr(last)
	return err == nil && start == netip.IPv6Unspecified() && end == netip.MustParseAddr("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")
}
