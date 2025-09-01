
//
//  RouteTableManager.m
//
//  Created by jianpx on 8/26/16.
//  Copyright © 2016 . All rights reserved.
//

#import "RouteTableManager.h"

//for sysctl fuction headers
#include <sys/types.h>
#include <sys/sysctl.h>

//contain CTL_NET for networking
#include <sys/socket.h>

//for route(4) https://developer.apple.com/legacy/library/documentation/Darwin/Reference/ManPages/man4/route.4.html#//apple_ref/doc/man/4/route
#include <net/if.h>

//for converting address
#include <net/if_dl.h>
#include <sys/ioctl.h>

@implementation RouteTableManager

+ (NSArray<RouteRecord *> *)getAllRoutes {
    NSMutableArray* routeArray = [NSMutableArray array];
    RouteRecord* route = nil;

    int mibSize = 6;
    size_t len;
    int mib[mibSize];
    char *buf;
    register struct rt_msghdr2 *rtm;
    
    mib[0] = CTL_NET;
    //Return the entire routing table or a subset of it
    mib[1] = PF_ROUTE; //PF_INET or PF_INET6
    mib[2] = 0; //protocol number, currently always 0.
    mib[3] = 0; //The fourth level name is an address family, which may be set to 0 to select all address families

    //another fifth level value
    //#define NET_RT_DUMP		1	/* dump; may limit to a.f. */
    //#define NET_RT_FLAGS		2	/* by flags, e.g. RESOLVING */
    //#define NET_RT_IFLIST		3	/* survey interface list */
    //#define NET_RT_STAT		4	/* routing statistics */
    //#define NET_RT_TRASH		5	/* routes not in table but not freed */
    //#define NET_RT_IFLIST2	6	/* interface list with addresses */
    //#define NET_RT_DUMP2		7	/* dump; may limit to a.f. */
    mib[4] = NET_RT_DUMP2;
    mib[5] = 0;

    //make the buff NULL and call sysctl can get the buff size to &len
    if (sysctl(mib, mibSize, NULL, &len, NULL, 0) < 0) {
        return nil;
    }
    if (len <= 0) {
        return nil;
    }

    if ((buf = malloc(len)) == 0) {
        NSLog(@"malloc %ld buffer error", (long)len);
        return nil;
    };

    if (buf && sysctl(mib, mibSize, buf, &len, NULL, 0) == 0)
    {
        //逐个rtm_msglen的长度移动，来读取每条路由记录
        for (char * ptr = buf; ptr < buf + len; ptr += rtm->rtm_msglen)
        {
            rtm = (struct rt_msghdr2 *)ptr;

            struct sockaddr* dst_sa = (struct sockaddr *)(rtm + 1);
            if(rtm->rtm_addrs & RTA_DST)
            {
            	//Don't print protocol-cloned routes unless -a.
                if(dst_sa->sa_family == AF_INET && !((rtm->rtm_flags & RTF_WASCLONED) && (rtm->rtm_parentflags & RTF_PRCLONING)))
                {
                    route = [[RouteRecord alloc] initWithRtm:rtm];
                    if(route != nil)
                    {
                        [routeArray addObject:route];
                    }
                }
            }

        }
    }

    free(buf);
    
    return routeArray;
}

+ (NSString *)formatRouteTable {
    NSArray<RouteRecord *> *routes = [RouteTableManager getAllRoutes];
    NSMutableString *format = [[NSMutableString alloc] initWithFormat:@"%ld records\n", (long)routes.count];
    NSArray *headers = @[@"Destination", @"Gateway", @"Flags", @"Refs", @"Use", @"Mtu", @"Netif", @"Expire"];
    NSMutableArray<NSNumber *> *dstArray = [@[] mutableCopy];
    NSMutableArray<NSNumber *> *gatewayArray = [@[] mutableCopy];
    NSMutableArray<NSNumber *> *flagsArray = [@[] mutableCopy];
    NSMutableArray<NSNumber *> *refsArray = [@[] mutableCopy];
    NSMutableArray<NSNumber *> *useArray = [@[] mutableCopy];
    NSMutableArray<NSNumber *> *mtuArray = [@[] mutableCopy];
    NSMutableArray<NSNumber *> *netifArray = [@[] mutableCopy];
    NSMutableArray<NSNumber *> *expireArray = [@[] mutableCopy];

    for (RouteRecord *r in routes) {
        [dstArray addObject:@(r.destination.length)];
        [gatewayArray addObject:@(r.gateway.length)];
        [flagsArray addObject:@(r.flags.length)];
        [refsArray addObject:@([[@(r.refs) stringValue] length])];
        [useArray addObject:@([[@(r.use) stringValue] length])];
        [mtuArray addObject:@([[@(r.mtu) stringValue] length])];
        [netifArray addObject:@(r.netif.length)];
        [expireArray addObject:@([[@(r.expire) stringValue] length])];
    }

    NSNumber *dstMaxLength = [dstArray valueForKeyPath:@"@max.integerValue"];
    NSNumber *gatewayMaxLength = [gatewayArray valueForKeyPath:@"@max.integerValue"];
    NSNumber *flagsMaxLength = [flagsArray valueForKeyPath:@"@max.integerValue"];
    NSNumber *refsMaxLength = [refsArray valueForKeyPath:@"@max.integerValue"];
    NSNumber *useMaxLength = [useArray valueForKeyPath:@"@max.integerValue"];
    NSNumber *mtuMaxLength = [mtuArray valueForKeyPath:@"@max.integerValue"];
    NSNumber *netifMaxLength = [netifArray valueForKeyPath:@"@max.integerValue"];
    NSNumber *expireMaxLength = [expireArray valueForKeyPath:@"@max.integerValue"];

    NSInteger dstLength = 0;
    NSInteger gatewayLength = 0j;
    NSInteger flagsLength = 0;
    NSInteger refsLength = 0;
    NSInteger useLength = 0;
    NSInteger mtuLength = 0;
    NSInteger netifLength = 0;
    NSInteger expireLength = 0;
    NSInteger padding = 3;

    for (NSString *hdr in headers) {
        NSInteger targetLength = hdr.length;
        if ([hdr isEqualToString:@"Destination"]) {
            dstLength = MAX([dstMaxLength integerValue], [@"Destination" length]) + padding;
            targetLength = dstLength;
        } else if ([hdr isEqualToString:@"Gateway"]) {
            gatewayLength = MAX([gatewayMaxLength integerValue], [@"Gateway" length]) + padding;
            targetLength = gatewayLength;
        } else if ([hdr isEqualToString:@"Flags"]) {
            flagsLength = MAX([flagsMaxLength integerValue], [@"Flags" length]) + padding;
            targetLength = flagsLength;
        } else if ([hdr isEqualToString:@"Refs"]) {
            refsLength = MAX([refsMaxLength integerValue], [@"Refs" length]) + padding;
            targetLength = refsLength;
        } else if ([hdr isEqualToString:@"Use"]) {
            useLength = MAX([useMaxLength integerValue], [@"Use" length]) + padding;
            targetLength = useLength;
        } else if ([hdr isEqualToString:@"Mtu"]) {
            mtuLength = MAX([mtuMaxLength integerValue], [@"Mtu" length]) + padding;
            targetLength = mtuLength;
        } else if ([hdr isEqualToString:@"Netif"]) {
            netifLength = MAX([netifMaxLength integerValue], [@"Netif" length]) + padding;
            targetLength = netifLength;
        } else if ([hdr isEqualToString:@"Expire"]) {
            expireLength = MAX([expireMaxLength integerValue], [@"Expire" length]) + padding;
            targetLength = expireLength;
        }
        NSString *h = [hdr stringByPaddingToLength:targetLength withString:@" " startingAtIndex:0];
        [format appendString:h];
    }
    [format appendString:@"\n"];

    for (RouteRecord *r in routes) {
        NSString *dst = [r.destination stringByPaddingToLength:dstLength withString:@" " startingAtIndex:0];
        NSString *gateway = [r.gateway stringByPaddingToLength:gatewayLength withString:@" " startingAtIndex:0];
        NSString *flags = [r.flags stringByPaddingToLength:flagsLength withString:@" " startingAtIndex:0];
        NSString *refs = [[@(r.refs) stringValue] stringByPaddingToLength:refsLength withString:@" " startingAtIndex:0];
        NSString *use = [[@(r.use) stringValue] stringByPaddingToLength:useLength withString:@" " startingAtIndex:0];
        NSString *mtu = [[@(r.mtu) stringValue] stringByPaddingToLength:mtuLength withString:@" " startingAtIndex:0];
        NSString *expire = [[@(r.expire) stringValue] stringByPaddingToLength:expireLength withString:@" " startingAtIndex:0];
        NSString *netif = [r.netif stringByPaddingToLength:netifLength withString:@" " startingAtIndex:0];
        [format appendString:dst];
        [format appendString:gateway];
        [format appendString:flags];
        [format appendString:refs];
        [format appendString:use];
        [format appendString:mtu];
        [format appendString:netif];
        [format appendString:expire];
        [format appendString:@"\n"];
    }
    return format;
}
@end
