//go:build windows

package routing

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

const (
	nrptLocalPath = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
	// Group Policy rules live here; while any exist, Windows ignores local rules.
	nrptPolicyPath = `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`
)

var (
	procDnsFlushResolverCache = windows.NewLazySystemDLL("dnsapi.dll").NewProc("DnsFlushResolverCache")
	procNtDeleteKey           = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtDeleteKey")
)

// keyDelete is the standard DELETE access right, needed by NtDeleteKey.
const keyDelete = 0x00010000

type registryNRPTStore struct{}

type registryNRPTKey struct{ registry.Key }

func (registryNRPTStore) Open(key string, create bool) (nrptKey, error) {
	path := nrptLocalPath + `\` + key
	access := uint32(registry.QUERY_VALUE | registry.SET_VALUE | keyDelete)
	var k registry.Key
	var err error
	if create {
		k, _, err = registry.CreateKey(registry.LOCAL_MACHINE, path, access)
	} else {
		k, err = registry.OpenKey(registry.LOCAL_MACHINE, path, access)
		if errors.Is(err, registry.ErrNotExist) {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return registryNRPTKey{k}, nil
}

func (k registryNRPTKey) Values() (nrptValues, error) {
	names, err := k.ReadValueNames(0)
	if err != nil {
		return nil, err
	}
	values := nrptValues{}
	for _, name := range names {
		_, kind, err := k.GetValue(name, nil)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		switch kind {
		case registry.DWORD:
			value, _, err := k.GetIntegerValue(name)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			values[name] = uint32(value)
		case registry.SZ, registry.EXPAND_SZ:
			value, _, err := k.GetStringValue(name)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			values[name] = value
		case registry.MULTI_SZ:
			value, _, err := k.GetStringsValue(name)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			values[name] = value
		default:
			values[name] = fmt.Sprintf("registry value type %d", kind)
		}
	}
	return values, nil
}

func (k registryNRPTKey) Set(name string, value nrptValue) error {
	switch value := value.(type) {
	case uint32:
		return k.SetDWordValue(name, value)
	case string:
		return k.SetStringValue(name, value)
	case []string:
		return k.SetStringsValue(name, value)
	default:
		return fmt.Errorf("unsupported NRPT value %s of type %T", name, value)
	}
}

func (k registryNRPTKey) DeleteValue(name string) error {
	if err := k.Key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// DeleteIfEmpty deletes the key object behind this handle. NtDeleteKey takes
// the handle, not a name, so a key that someone else put under the same name
// after this handle was opened is never deleted. Values added to this same key
// object between the check and the delete would still go with it.
func (k registryNRPTKey) DeleteIfEmpty() error {
	info, err := k.Stat()
	if err != nil {
		return err
	}
	if info.ValueCount != 0 || info.SubKeyCount != 0 {
		return fmt.Errorf("NRPT rule key has %d values and %d subkeys; not deleted", info.ValueCount, info.SubKeyCount)
	}
	if err := procNtDeleteKey.Find(); err != nil {
		return err
	}
	if status, _, _ := procNtDeleteKey.Call(uintptr(k.Key)); status != 0 {
		return fmt.Errorf("NtDeleteKey: %w", windows.NTStatus(status))
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

// nrptOwnerMutex serializes NRPT changes between DobbyVPN processes. The
// process that holds it owns the rule; it is held until the process exits, so
// Windows marks it abandoned if that process crashes.
const nrptOwnerMutex = `Global\DobbyVPN-NRPT-Owner`

var nrptOwner struct {
	sync.Mutex
	held bool
}

// AcquireWindowsNRPTOwnership waits up to timeout for the owner mutex and then
// holds it until the process exits. previousCrashed reports that the last
// holder exited without releasing it. Repeated calls after success return at
// once.
func AcquireWindowsNRPTOwnership(timeout time.Duration) (previousCrashed bool, err error) {
	nrptOwner.Lock()
	defer nrptOwner.Unlock()
	if nrptOwner.held {
		return false, nil
	}
	type result struct {
		crashed bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		// A Windows mutex belongs to a thread. This goroutine keeps its thread
		// for the life of the process so the mutex is never abandoned early.
		runtime.LockOSThread()
		name, err := windows.UTF16PtrFromString(nrptOwnerMutex)
		if err != nil {
			runtime.UnlockOSThread()
			done <- result{err: err}
			return
		}
		handle, err := windows.CreateMutexEx(nil, name, 0, windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE)
		if err != nil {
			runtime.UnlockOSThread()
			done <- result{err: fmt.Errorf("open %s: %w", nrptOwnerMutex, err)}
			return
		}
		event, err := windows.WaitForSingleObject(handle, uint32(timeout/time.Millisecond))
		switch {
		case err == nil && (event == windows.WAIT_OBJECT_0 || event == windows.WAIT_ABANDONED):
			done <- result{crashed: event == windows.WAIT_ABANDONED}
			for { // hold the mutex and this thread until the process exits
				time.Sleep(time.Hour)
			}
		case err == nil && event == uint32(windows.WAIT_TIMEOUT):
			err = errors.New("another DobbyVPN process owns the NRPT rule")
		case err == nil:
			err = fmt.Errorf("wait for %s returned %#x", nrptOwnerMutex, event)
		}
		_ = windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		done <- result{err: err}
	}()
	r := <-done
	nrptOwner.held = r.err == nil
	return r.crashed, r.err
}

func requireNRPTOwnership() error {
	_, err := AcquireWindowsNRPTOwnership(0)
	return err
}

// InstallWindowsNRPT adds the owned rule that sends all names to servers.
func InstallWindowsNRPT(servers []netip.Addr) error {
	if err := requireNRPTOwnership(); err != nil {
		return err
	}
	return installOwnedNRPT(windowsNRPTStore, servers)
}

// RemoveWindowsNRPT removes the owned rule. It reports whether one was present.
func RemoveWindowsNRPT() (bool, error) {
	if err := requireNRPTOwnership(); err != nil {
		return false, err
	}
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
	if values, err := readNRPTKey(windowsNRPTStore); err != nil {
		fields["nrpt_owned_error"] = err.Error()
	} else {
		fields["nrpt_owned_present"] = values != nil && ownedNRPTKey(values)
		fields["nrpt_owned_complete"] = values != nil && values["ConfigOptions"] == uint32(nrptGenericServer)
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
