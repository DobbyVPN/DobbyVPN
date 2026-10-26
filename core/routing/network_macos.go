//go:build darwin && !ios && cgo

package routing

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <net/if.h>
#include <stdlib.h>

typedef struct {
 unsigned int ipv4, ipv6;
 CFDataRef snapshot;
} dobby_physical_network;

// Copy only the physical primary services. The supplemental VPN DNS service
// never participates in this snapshot, and no global DNS setting is consulted.
static int dobby_physical_capture(dobby_physical_network *result) {
 int status = kSCStatusOK;
 SCDynamicStoreRef store = SCDynamicStoreCreate(NULL,CFSTR("DobbyVPN underlying network"),NULL,NULL);
 if (store == NULL) return SCError();
 const void *globalNames[] = {CFSTR("State:/Network/Global/IPv4"),CFSTR("State:/Network/Global/IPv6")};
 CFArrayRef globals = CFArrayCreate(NULL,globalNames,2,&kCFTypeArrayCallBacks);
 CFDictionaryRef before = SCDynamicStoreCopyMultiple(store,globals,NULL);
 CFMutableArrayRef keys = CFArrayCreateMutableCopy(NULL,0,globals);
 CFDictionaryRef snapshot = NULL;
 CFDictionaryRef after = NULL;
 if (before == NULL) { status=SCError(); goto done; }
 for (int family=0;family<2;family++) {
  CFDictionaryRef global = CFDictionaryGetValue(before,globalNames[family]);
  if (global == NULL) continue;
  if (CFGetTypeID(global) != CFDictionaryGetTypeID()) { status=kSCStatusInvalidArgument; goto done; }
  CFStringRef interfaceName = CFDictionaryGetValue(global,kSCDynamicStorePropNetPrimaryInterface);
  CFStringRef service = CFDictionaryGetValue(global,kSCDynamicStorePropNetPrimaryService);
  if (interfaceName == NULL || service == NULL || CFGetTypeID(interfaceName)!=CFStringGetTypeID() || CFGetTypeID(service)!=CFStringGetTypeID()) { status=kSCStatusNoLink;goto done; }
  char name[IF_NAMESIZE];
  if (!CFStringGetCString(interfaceName,name,sizeof(name),kCFStringEncodingUTF8)) {status=kSCStatusInvalidArgument;goto done;}
  unsigned int index=if_nametoindex(name);
  if (index==0) {status=kSCStatusNoLink;goto done;}
  if (family==0) result->ipv4=index; else result->ipv6=index;
  const CFStringRef entities[]={kSCEntNetIPv4,kSCEntNetIPv6,kSCEntNetDNS};
  for (int entity=0;entity<3;entity++) {
   CFStringRef key=SCDynamicStoreKeyCreateNetworkServiceEntity(NULL,kSCDynamicStoreDomainState,service,entities[entity]);
   CFArrayAppendValue(keys,key);CFRelease(key);
  }
 }
 if (result->ipv4==0 && result->ipv6==0) {status=kSCStatusNoLink;goto done;}
 snapshot=SCDynamicStoreCopyMultiple(store,keys,NULL);
 if (snapshot==NULL) {status=SCError();goto done;}
 after=SCDynamicStoreCopyMultiple(store,globals,NULL);
 if (after==NULL) {status=SCError();goto done;}
 if (!CFEqual(before,after)) {status=kSCStatusStale;goto done;}
 result->snapshot=CFPropertyListCreateData(NULL,snapshot,kCFPropertyListXMLFormat_v1_0,0,NULL);
 if (result->snapshot==NULL) status=kSCStatusFailed;
 done:
 if (after!=NULL) CFRelease(after);
 if (snapshot!=NULL) CFRelease(snapshot);
 if (keys!=NULL) CFRelease(keys);
 if (before!=NULL) CFRelease(before);
 CFRelease(globals);CFRelease(store);
 return status;
}
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"unsafe"
)

type MacOSPhysicalNetwork struct {
	IPv4, IPv6 int
	Identity   string
}

func CaptureMacOSPhysicalNetwork(ctx context.Context) (MacOSPhysicalNetwork, error) {
	if err := ctx.Err(); err != nil {
		return MacOSPhysicalNetwork{}, err
	}
	var captured C.dobby_physical_network
	status := C.dobby_physical_capture(&captured)
	if captured.snapshot != 0 {
		defer C.CFRelease(C.CFTypeRef(captured.snapshot))
	}
	if status != 0 {
		return MacOSPhysicalNetwork{}, fmt.Errorf("capture physical macOS network: SystemConfiguration status %d", int(status))
	}
	for _, index := range []int{int(captured.ipv4), int(captured.ipv6)} {
		if index == 0 {
			continue
		}
		iface, err := net.InterfaceByIndex(index)
		if err != nil {
			return MacOSPhysicalNetwork{}, err
		}
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || strings.HasPrefix(iface.Name, "utun") {
			return MacOSPhysicalNetwork{}, errors.New("native primary service is not an active physical interface")
		}
	}
	length := C.CFDataGetLength(captured.snapshot)
	if length <= 0 || uint64(length) > math.MaxInt32 {
		return MacOSPhysicalNetwork{}, errors.New("invalid physical network snapshot length")
	}
	snapshot := C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(captured.snapshot)), C.int(length))
	if err := ctx.Err(); err != nil {
		return MacOSPhysicalNetwork{}, err
	}
	return MacOSPhysicalNetwork{int(captured.ipv4), int(captured.ipv6), fmt.Sprintf("macos:%x", sha256.Sum256(snapshot))}, nil
}
