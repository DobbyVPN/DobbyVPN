//go:build darwin && !ios && cgo

package routing

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>

typedef struct {
 SCDynamicStoreRef store;
 CFStringRef key;
 CFDictionaryRef value;
} dobby_dns_owner;

static int dobby_dns_release(dobby_dns_owner *owner) {
 if (owner == NULL) return 0;
 if (owner->store != NULL && owner->key != NULL) {
  CFPropertyListRef current = SCDynamicStoreCopyValue(owner->store, owner->key);
  if (current != NULL) {
   Boolean ours = owner->value != NULL && CFEqual(current, owner->value);
   CFRelease(current);
   if (!ours) return kSCStatusKeyExists;
   if (!SCDynamicStoreRemoveValue(owner->store, owner->key)) return SCError();
  } else {
   int status = SCError();
   if (status != kSCStatusNoKey) return status == kSCStatusOK ? kSCStatusFailed : status;
  }
 }
 if (owner->value != NULL) CFRelease(owner->value);
 if (owner->key != NULL) CFRelease(owner->key);
 if (owner->store != NULL) CFRelease(owner->store);
 free(owner);
 return 0;
}

static int dobby_dns_install(const char *iface, const char *servers, dobby_dns_owner **result) {
 dobby_dns_owner *owner = calloc(1, sizeof(*owner));
 if (owner == NULL) return kSCStatusFailed;
 *result = owner;
 const void *optionKeys[] = {kSCDynamicStoreUseSessionKeys};
 const void *optionValues[] = {kCFBooleanTrue};
 CFDictionaryRef options = CFDictionaryCreate(NULL,optionKeys,optionValues,1,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 owner->store = SCDynamicStoreCreateWithOptions(NULL,CFSTR("DobbyVPN tunnel DNS"),options,NULL,NULL);
 CFRelease(options);
 if (owner->store == NULL) return SCError();
 CFUUIDRef uuid = CFUUIDCreate(NULL);
 CFStringRef identifier = CFUUIDCreateString(NULL,uuid);
 owner->key = CFStringCreateWithFormat(NULL,NULL,CFSTR("State:/Network/Service/DobbyVPN-%@/DNS"),identifier);
 CFRelease(identifier);
 CFRelease(uuid);
 CFStringRef interfaceName = CFStringCreateWithCString(NULL,iface,kCFStringEncodingUTF8);
 CFStringRef serverText = CFStringCreateWithCString(NULL,servers,kCFStringEncodingUTF8);
 CFArrayRef serverArray = CFStringCreateArrayBySeparatingStrings(NULL,serverText,CFSTR(","));
 const void *root[] = {CFSTR("")};
 CFArrayRef domains = CFArrayCreate(NULL,root,1,&kCFTypeArrayCallBacks);
 int order = 0;
 CFNumberRef orderValue = CFNumberCreate(NULL,kCFNumberIntType,&order);
 const void *orders[] = {orderValue};
 CFArrayRef matchOrders = CFArrayCreate(NULL,orders,1,&kCFTypeArrayCallBacks);
 const void *keys[] = {kSCPropNetDNSServerAddresses,kSCPropNetDNSSupplementalMatchDomains,kSCPropNetDNSSupplementalMatchOrders,CFSTR("InterfaceName")};
 const void *values[] = {serverArray,domains,matchOrders,interfaceName};
 owner->value = CFDictionaryCreate(NULL,keys,values,4,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFRelease(matchOrders); CFRelease(orderValue); CFRelease(domains); CFRelease(serverArray); CFRelease(serverText); CFRelease(interfaceName);
 if (!SCDynamicStoreSetValue(owner->store,owner->key,owner->value)) return SCError();
 return 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unsafe"
)

// AcquireMacOSDNS publishes a separate, session-owned supplemental resolver.
// It never replaces physical service settings; dynamic-store session keys are
// also released by the OS when this backend exits.
func (p *Plan) AcquireMacOSDNS(ctx context.Context, iface string, servers []string) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if iface == "" || len(servers) == 0 {
		return nil, errors.New("tunnel DNS requires an interface and servers")
	}
	for _, server := range servers {
		if _, err := netip.ParseAddr(server); err != nil {
			return nil, err
		}
	}
	var owner *C.dobby_dns_owner
	lease, err := p.Acquire("tunnel-dns "+iface, func() error { return nil }, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if owner == nil {
			return nil
		}
		if code := C.dobby_dns_release(owner); code != 0 {
			return fmt.Errorf("release owned macOS DNS: SystemConfiguration status %d", int(code))
		}
		owner = nil
		return nil
	})
	if err != nil {
		return nil, err
	}
	name := C.CString(iface)
	defer C.free(unsafe.Pointer(name))
	addresses := C.CString(strings.Join(servers, ","))
	defer C.free(unsafe.Pointer(addresses))
	if code := C.dobby_dns_install(name, addresses, &owner); code != 0 {
		return lease, fmt.Errorf("install owned macOS DNS: SystemConfiguration status %d", int(code))
	}
	return lease, nil
}
