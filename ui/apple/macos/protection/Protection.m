#import <Foundation/Foundation.h>
#import <Network/Network.h>
#import <NetworkExtension/NetworkExtension.h>
#import <os/log.h>
#import <stdatomic.h>
#import <netinet/in.h>
#import "TunnelLease.h"

static uint16_t read16(const uint8_t *bytes) {
    return ((uint16_t)bytes[0] << 8) | bytes[1];
}

// Permit link address acquisition while application traffic is paused. The
// packet provider receives Ethernet frames, including frames with VLAN tags.
static bool isLinkMaintenance(const uint8_t *frame, size_t size) {
    if (!frame || size < 14) return false;
    uint16_t type = read16(frame + 12);
    size_t start = 14;
    while (type == 0x8100 || type == 0x88a8) {
        if (size - start < 4) return false;
        type = read16(frame + start + 2);
        start += 4;
    }
    const uint8_t *ip = frame + start;
    const size_t length = size - start;
    if (type == 0x0806) {
        return length >= 28 && read16(ip) == 1 && read16(ip + 2) == 0x0800 &&
               ip[4] == 6 && ip[5] == 4 && (read16(ip + 6) == 1 || read16(ip + 6) == 2);
    }
    if (type == 0x0800 && length >= 20 && (ip[0] >> 4) == 4) {
        size_t header = (ip[0] & 15) * 4U;
        return header >= 20 && length >= header + 8 && ip[9] == IPPROTO_UDP &&
               (read16(ip + 6) & 0x3fff) == 0 &&
               read16(ip + header) == 68 && read16(ip + header + 2) == 67;
    }
    if (type == 0x86dd && length >= 48 && (ip[0] >> 4) == 6) {
        if (ip[6] == IPPROTO_ICMPV6)
            return ip[7] == 255 && ip[40] >= 133 && ip[40] <= 136 && ip[41] == 0;
        return ip[6] == IPPROTO_UDP && read16(ip + 40) == 546 && read16(ip + 42) == 547;
    }
    return false;
}

@interface DobbyProtection : NEFilterPacketProvider
@end

@implementation DobbyProtection
- (void)startFilterWithCompletionHandler:(void (^)(NSError *))complete {
    int fd = DobbyOpenProtectionLease(false);
    if (fd < 0) {
        complete([NSError errorWithDomain:NSPOSIXErrorDomain code:errno userInfo:nil]);
        return;
    }
    NSFileHandle *lease = [[NSFileHandle alloc] initWithFileDescriptor:fd closeOnDealloc:YES];
    os_log_t logger = os_log_create("vpn.dobby.desktop.Protection", "egress");
    static atomic_uint drops;
    self.packetHandler = ^NEFilterPacketProviderVerdict(NEFilterPacketContext *ctx, nw_interface_t link,
                    NETrafficDirection direction, const void *packet, size_t length) {
        if (direction == NETrafficDirectionInbound ||
            (link && nw_interface_get_type(link) == nw_interface_type_loopback) ||
            DobbyTunnelOwnsLease(lease.fileDescriptor) || isLinkMaintenance(packet, length)) {
            return NEFilterPacketProviderVerdictAllow;
        }
        unsigned count = atomic_fetch_add(&drops, 1) + 1;
        if (count && (count & (count - 1)) == 0) {
            const char *name = link ? nw_interface_get_name(link) : NULL;
            os_log(logger, "Tunnel routes unavailable, blocked outgoing frame on %{public}s, total %u",
                   name ? name : "unknown", count);
        }
        return NEFilterPacketProviderVerdictDrop;
    };
    if (!DobbyMarkFilterReady(fd)) {
        NSError *error = [NSError errorWithDomain:NSPOSIXErrorDomain code:errno userInfo:nil];
        self.packetHandler = nil;
        complete(error);
        return;
    }
    os_log(logger, "Physical traffic protection is active");
    complete(nil);
}

- (void)stopFilterWithReason:(NEProviderStopReason)reason completionHandler:(void (^)(void))complete {
    self.packetHandler = nil;
    os_log(OS_LOG_DEFAULT, "DobbyVPN packet protection stopped, reason %ld", (long)reason);
    complete();
}
@end

int main(void) {
    @autoreleasepool {
        [NEProvider startSystemExtensionMode];
        dispatch_main();
    }
}
