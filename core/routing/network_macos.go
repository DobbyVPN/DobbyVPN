//go:build darwin && !ios && cgo

package routing

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
 unsigned int ipv4, ipv6;
 CFDataRef snapshot;
 char dns[1024];
} dobby_physical_network;

// Comma-separated ServerAddresses of the captured physical DNS entities.
static CFArrayRef dobby_dns_servers(CFDictionaryRef snapshot, CFStringRef key) {
 if (key == NULL) return NULL;
 CFDictionaryRef service = CFDictionaryGetValue(snapshot, key);
 if (service == NULL || CFGetTypeID(service) != CFDictionaryGetTypeID()) return NULL;
 CFArrayRef servers = CFDictionaryGetValue(service, kSCPropNetDNSServerAddresses);
 if (servers == NULL || CFGetTypeID(servers) != CFArrayGetTypeID() || CFArrayGetCount(servers) == 0) return NULL;
 return servers;
}

static void dobby_physical_dns(CFDictionaryRef snapshot, CFStringRef *state, CFStringRef *setup, char *out, size_t size) {
 size_t used = 0;
 out[0] = 0;
 for (int family = 0; family < 2; family++) {
  CFArrayRef servers = dobby_dns_servers(snapshot, state[family]);
  // Static DNS can be present only in Setup for the same primary service.
  if (servers == NULL) servers = dobby_dns_servers(snapshot, setup[family]);
  if (servers == NULL) continue;
  for (CFIndex j = 0; j < CFArrayGetCount(servers); j++) {
   CFStringRef server = CFArrayGetValueAtIndex(servers, j);
   char text[96];
   if (CFGetTypeID(server) != CFStringGetTypeID() || !CFStringGetCString(server, text, sizeof(text), kCFStringEncodingUTF8)) continue;
   size_t length = strlen(text);
   if (used + length + 2 > size) break;
   memcpy(out + used, text, length);
   used += length;
   out[used++] = ',';
   out[used] = 0;
  }
 }
}

// A configured service can have only a self-assigned address. It cannot
// carry the default route just because it precedes another live service.
static int dobby_has_uplink(const struct ifaddrs *links, const char *name, CFDictionaryRef ip, int family) {
 const CFStringRef routerKey = family == AF_INET ? kSCPropNetIPv4Router : kSCPropNetIPv6Router;
 const CFStringRef peersKey = family == AF_INET ? kSCPropNetIPv4DestAddresses : kSCPropNetIPv6DestAddresses;
 CFStringRef router = CFDictionaryGetValue(ip, routerKey);
 CFArrayRef peers = CFDictionaryGetValue(ip, peersKey);
 for (const struct ifaddrs *link = links; link; link = link->ifa_next) {
  if (!link->ifa_addr || link->ifa_addr->sa_family != family || strcmp(link->ifa_name, name) != 0) continue;
  if ((link->ifa_flags & (IFF_UP | IFF_RUNNING | IFF_LOOPBACK)) != (IFF_UP | IFF_RUNNING)) continue;
  if (router && CFGetTypeID(router) == CFStringGetTypeID() && CFStringGetLength(router)) return 1;
  if ((link->ifa_flags & IFF_POINTOPOINT) && peers && CFGetTypeID(peers) == CFArrayGetTypeID() && CFArrayGetCount(peers)) return 1;
 }
 return 0;
}

// All-network VPN routing promotes the utun to the global primary. Select
// live physical services in the user's network service order and read their
// IP and DNS entities from the same dynamic-store snapshot.
static int dobby_provider_capture(dobby_physical_network *result) {
 SCDynamicStoreRef store = SCDynamicStoreCreate(NULL, CFSTR("DobbyVPN provider uplink"), NULL, NULL);
 if (store == NULL) return SCError();
 CFStringRef orderKey = CFSTR("Setup:/Network/Global/IPv4");
 const void *patternValues[] = {CFSTR("State:/Network/Service/[^/]+/(IPv4|IPv6|DNS)"), CFSTR("Setup:/Network/Service/[^/]+/DNS")};
 CFArrayRef patterns = CFArrayCreate(NULL, patternValues, 2, &kCFTypeArrayCallBacks);
 const void *orderKeys[] = {orderKey};
 CFArrayRef keys = CFArrayCreate(NULL, orderKeys, 1, &kCFTypeArrayCallBacks);
 CFDictionaryRef snapshot = SCDynamicStoreCopyMultiple(store, keys, patterns);
 CFRelease(patterns); CFRelease(keys); CFRelease(store);
 if (snapshot == NULL) return kSCStatusFailed;
 CFDictionaryRef order = CFDictionaryGetValue(snapshot, orderKey);
 CFArrayRef services = order && CFGetTypeID(order) == CFDictionaryGetTypeID()
     ? CFDictionaryGetValue(order, kSCPropNetServiceOrder) : NULL;
 CFMutableDictionaryRef selected = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
 CFStringRef stateDNS[2] = {NULL, NULL}, setupDNS[2] = {NULL, NULL};
 const CFStringRef families[] = {kSCEntNetIPv4, kSCEntNetIPv6};
 int status = kSCStatusNoLink;
 struct ifaddrs *links = NULL;
 if (services && CFGetTypeID(services) == CFArrayGetTypeID() && getifaddrs(&links) == 0) {
  for (int f=0; f<2; f++) {
   for (CFIndex i=0; i<CFArrayGetCount(services); i++) {
    CFStringRef service = CFArrayGetValueAtIndex(services, i);
    if (CFGetTypeID(service) != CFStringGetTypeID()) continue;
    CFStringRef key = SCDynamicStoreKeyCreateNetworkServiceEntity(NULL, kSCDynamicStoreDomainState, service, families[f]);
    CFDictionaryRef ip = CFDictionaryGetValue(snapshot, key);
    if (!ip || CFGetTypeID(ip) != CFDictionaryGetTypeID()) { CFRelease(key); continue; }
    CFStringRef iface = CFDictionaryGetValue(ip, kSCPropInterfaceName);
    char name[IF_NAMESIZE];
    if (!iface || CFGetTypeID(iface) != CFStringGetTypeID() ||
        !CFStringGetCString(iface,name,sizeof(name),kCFStringEncodingUTF8) ||
        strncmp(name,"utun",4)==0 || strcmp(name,"lo0")==0) { CFRelease(key); continue; }
    unsigned int index=if_nametoindex(name);
    if (!index || !dobby_has_uplink(links, name, ip, f == 0 ? AF_INET : AF_INET6)) { CFRelease(key); continue; }
    if (f==0) result->ipv4=index; else result->ipv6=index;
    CFDictionarySetValue(selected,key,ip);
    CFRelease(key);
    stateDNS[f]=SCDynamicStoreKeyCreateNetworkServiceEntity(NULL,kSCDynamicStoreDomainState,service,kSCEntNetDNS);
    setupDNS[f]=SCDynamicStoreKeyCreateNetworkServiceEntity(NULL,kSCDynamicStoreDomainSetup,service,kSCEntNetDNS);
    const CFStringRef dnsKeys[]={stateDNS[f],setupDNS[f]};
    for(int j=0;j<2;j++) {
     CFTypeRef value=CFDictionaryGetValue(snapshot,dnsKeys[j]);
     if(value) CFDictionarySetValue(selected,dnsKeys[j],value);
    }
    break;
   }
  }
  if (result->ipv4 || result->ipv6) {
   dobby_physical_dns(snapshot,stateDNS,setupDNS,result->dns,sizeof(result->dns));
   result->snapshot=CFPropertyListCreateData(NULL,selected,kCFPropertyListXMLFormat_v1_0,0,NULL);
   status=result->snapshot ? kSCStatusOK : kSCStatusFailed;
  }
 }
 for(int f=0;f<2;f++){if(stateDNS[f]) CFRelease(stateDNS[f]); if(setupDNS[f]) CFRelease(setupDNS[f]);}
 if (links) freeifaddrs(links);
 CFRelease(selected); CFRelease(snapshot);
 return status;
}

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
 CFStringRef stateDNS[2] = {NULL, NULL};
 CFStringRef setupDNS[2] = {NULL, NULL};
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
   if (entity == 2) stateDNS[family] = key;
   CFArrayAppendValue(keys,key);CFRelease(key);
  }
  CFStringRef key=SCDynamicStoreKeyCreateNetworkServiceEntity(NULL,kSCDynamicStoreDomainSetup,service,kSCEntNetDNS);
  setupDNS[family] = key;
  CFArrayAppendValue(keys,key);CFRelease(key);
 }
 if (result->ipv4==0 && result->ipv6==0) {status=kSCStatusNoLink;goto done;}
 snapshot=SCDynamicStoreCopyMultiple(store,keys,NULL);
 if (snapshot==NULL) {status=SCError();goto done;}
 after=SCDynamicStoreCopyMultiple(store,globals,NULL);
 if (after==NULL) {status=SCError();goto done;}
 if (!CFEqual(before,after)) {status=kSCStatusStale;goto done;}
 dobby_physical_dns(snapshot,stateDNS,setupDNS,result->dns,sizeof(result->dns));
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
	"net/netip"
	"os/exec"
	"slices"
	"strings"
	"unsafe"
)

type MacOSPhysicalNetwork struct {
	IPv4, IPv6 int
	Identity   string
	// DNSServers are the physical services' resolvers; the identity covers them.
	DNSServers    []netip.Addr
	NAT64Prefixes []netip.Prefix
}

func CaptureMacOSPhysicalNetwork(ctx context.Context) (MacOSPhysicalNetwork, error) {
	return captureMacOSNetwork(ctx, false)
}

func CaptureMacOSProviderNetwork(ctx context.Context) (MacOSPhysicalNetwork, error) {
	return captureMacOSNetwork(ctx, true)
}

func captureMacOSNetwork(ctx context.Context, provider bool) (MacOSPhysicalNetwork, error) {
	if err := ctx.Err(); err != nil {
		return MacOSPhysicalNetwork{}, err
	}
	var captured C.dobby_physical_network
	var status C.int
	if provider {
		status = C.dobby_provider_capture(&captured)
	} else {
		status = C.dobby_physical_capture(&captured)
	}
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
	var servers []netip.Addr
	for _, text := range strings.Split(C.GoString(&captured.dns[0]), ",") {
		server, err := netip.ParseAddr(text)
		// Loopback servers (a local DNS proxy) stay: the kill switch passes lo0.
		if err != nil || server.IsUnspecified() || slices.Contains(servers, server) {
			continue
		}
		servers = append(servers, server)
	}
	var prefixes []netip.Prefix
	if captured.ipv6 != 0 {
		iface, err := net.InterfaceByIndex(int(captured.ipv6))
		if err != nil {
			return MacOSPhysicalNetwork{}, err
		}
		output, err := exec.CommandContext(ctx, "/sbin/ifconfig", iface.Name).CombinedOutput()
		if err != nil {
			return MacOSPhysicalNetwork{}, fmt.Errorf("capture physical NAT64 inventory: %w: %s", err, output)
		}
		prefixes, err = parseMacOSNAT64Prefixes(string(output))
		if err != nil {
			return MacOSPhysicalNetwork{}, err
		}
	}
	identity := fmt.Sprintf("macos:%x:nat64:%v", sha256.Sum256(snapshot), prefixes)
	return MacOSPhysicalNetwork{IPv4: int(captured.ipv4), IPv6: int(captured.ipv6), Identity: identity,
		DNSServers: servers, NAT64Prefixes: prefixes}, nil
}
