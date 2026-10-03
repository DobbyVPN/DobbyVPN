//go:build darwin && !(android || ios)

package platform_engine

import (
	"testing"
	"unsafe"
)

func TestDarwinAddressIoctlABI(t *testing.T) {
	if size := unsafe.Sizeof(macOSIPv4Alias{}); size != 64 {
		t.Fatalf("in_aliasreq size=%d, want Darwin ABI 64", size)
	}
	if size := unsafe.Sizeof(macOSIPv6Alias{}); size != 128 {
		t.Fatalf("in6_aliasreq size=%d, want Darwin ABI 128", size)
	}
	if size := unsafe.Sizeof(macOSInterfaceFlags{}); size != 32 {
		t.Fatalf("ifreq size=%d, want Darwin ABI 32", size)
	}
	if offset := unsafe.Offsetof(macOSIPv6Alias{}.Expires); offset != 104 {
		t.Fatalf("IPv6 lifetime offset=%d, want Darwin ABI 104", offset)
	}
}
