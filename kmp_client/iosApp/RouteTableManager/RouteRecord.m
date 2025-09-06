//
//  RouteRecord.m
//
//  Created by jianpx on 8/26/16.
//  Copyright © 2016 . All rights reserved.
//

#import "RouteRecord.h"

// ifaddrs
#import <ifaddrs.h>

// inet
#import <arpa/inet.h>
#include <netinet/in.h>
#include <net/if.h>
#include <net/if_dl.h>
#include <sys/ioctl.h>
#include <stdio.h>

//other
#include <sys/param.h>
#include <netdb.h>

#define ROUNDUP(a) \
       ((a) > 0 ? (1 + (((a) - 1) | (sizeof(uint32_t) - 1))) : sizeof(uint32_t))

typedef union {
    uint32_t dummy;		/* Helps align structure. */
    struct	sockaddr u_sa;
    u_short	u_data[128];
} sa_u;


@interface RouteRecord ()
{
//    struct sockaddr     m_addrs[RTAX_MAX];
    struct sockaddr     *rti_info[RTAX_MAX];
    struct rt_msghdr2   m_rtm;
}

@end

@implementation RouteRecord

- (instancetype)initWithRtm:(struct rt_msghdr2 *)rtm {
    if (!rtm) {
        return nil;
    }
    self = [super init];
    if (self) {
        struct sockaddr* sa = (struct sockaddr*)(rtm + 1);
        memcpy(&(m_rtm), rtm, sizeof(struct rt_msghdr2));
//        for (int i = 0; i < RTAX_MAX; i++) {
//            [self setAddr:&(sa[i]) index:i];
//        }
        get_rtaddrs(rtm->rtm_addrs, sa, rti_info);
    }

    return self;

}

- (NSString *)selfDescription {
    NSString *d = [NSString stringWithFormat:@"<%@ %p> dst:%@, gateway:%@, flags:%@, refs:%ld, use:%ld, mtu:%ld, netif:%@, expire:%ld", NSStringFromClass([self class]), self, self.destination, self.gateway, self.flags, (long)self.refs, (long)self.use, (long)self.mtu, self.netif, (long)self.expire];
    return d;
}

- (NSString *)description {
    return [self selfDescription];
}

- (NSString *)debugDescription {
    return [self selfDescription];
}

//- (void)setAddr:(struct sockaddr*)sa index:(int)rtax_index
//{
//    if(rtax_index >= 0 && rtax_index < RTAX_MAX)
//    {
//        memcpy(&(m_addrs[rtax_index]), sa, sizeof(struct sockaddr));
//    }
//}

/*
- (nullable NSString*)getAddrStringAtIndex:(int)rtaxIndex
{
    NSString * routeString = nil;
    struct sockaddr* sa = &(m_addrs[rtaxIndex]);
    int flagVal = 1 << rtaxIndex;
    
    if(!(m_rtm.rtm_addrs & flagVal))
    {
        return nil;
    }

    if(rtaxIndex >= 0 && rtaxIndex < RTAX_MAX)
    {
        switch(sa->sa_family)
        {
            case AF_INET:
            {
                //sockaddr如果是AF_INET family的话可以转化成sockaddr_in
                struct sockaddr_in* si = (struct sockaddr_in *)sa;
                if(si->sin_addr.s_addr == INADDR_ANY) {
                    routeString = @"default";
                } else {
                    //si->sin_addr是ip地址的整型形式的表示, 可以用inet_ntoa转成x.x.x.x的字符串形式
                    //http://beej.us/guide/bgnet/output/html/multipage/inet_ntoaman.html 提到inet_ntoa不能处理IPV6的地址
                    char *ipAddr = inet_ntoa(si->sin_addr);
                    if (ipAddr != NULL) {
                        routeString = [NSString stringWithCString:ipAddr encoding:NSASCIIStringEncoding];
                    }
                }
            }
                break;

            case AF_INET6:
            {
                //http://man7.org/linux/man-pages/man3/inet_ntop.3.html
                struct sockaddr_in6* si = (struct sockaddr_in6 *)sa;
                static const unsigned char localhost_bytes[] =
                { 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1 };
                if(memcmp(si->sin6_addr.s6_addr, localhost_bytes, 16) == 0) {
                    routeString = @"localhost"; //right?
                } else {
                    //http://beej.us/guide/bgnet/output/html/multipage/inet_ntopman.html
                    char ipStr[INET6_ADDRSTRLEN];
                    const char *ipAddr = inet_ntop(AF_INET6, &(si->sin6_addr), ipStr, INET6_ADDRSTRLEN);
                    if (ipAddr != NULL) {
                        routeString = [NSString stringWithCString:ipAddr encoding:NSASCIIStringEncoding];
                    }
                }
            }
                break;

            case AF_LINK:
            {
                struct sockaddr_dl* sdl = (struct sockaddr_dl*)sa;
                if(sdl->sdl_nlen + sdl->sdl_alen + sdl->sdl_slen == 0)
                {
                    routeString = [NSString stringWithFormat: @"link #%d", sdl->sdl_index];
                } else {
                    routeString = [NSString stringWithCString:link_ntoa(sdl) encoding:NSASCIIStringEncoding];
                }
            }
                break;

            default:
            {
                char a[3 * sa->sa_len];
                char *cp;
                char *sep = "";
                int i;
                
                if(sa->sa_len == 0)
                {
                    routeString = nil;
                }
                else
                {
                    a[0] = '\0';
                    for(i = 0, cp = a; i < sa->sa_len; i++)
                    {
                        cp += sprintf(cp, "%s%02x", sep, (unsigned char)sa->sa_data[i]);
                        sep = ":";
                    }
                    routeString = [NSString stringWithCString:a encoding:NSASCIIStringEncoding];
                }
            }
        }
    }
    
    return routeString;
}
 */

#pragma mark - methods from Apple OpenSource netstat IMP -> route.c

static void
get_rtaddrs(int addrs, struct sockaddr *sa, struct sockaddr **rti_info)
{
    int i;
    for (i = 0; i < RTAX_MAX; i++) {
        if (addrs & (1 << i)) {
            rti_info[i] = sa;
            sa = (struct sockaddr *)(ROUNDUP(sa->sa_len) + (char *)sa);
        } else {
            rti_info[i] = NULL;
        }
    }
}

char *
p_sockaddr(struct sockaddr *sa, struct sockaddr *mask, int flags)
{
    char workbuf[128], *cplim;
    char *cp = workbuf;

    switch(sa->sa_family) {
        case AF_INET: {
            struct sockaddr_in *sin = (struct sockaddr_in *)sa;

            if ((sin->sin_addr.s_addr == INADDR_ANY) &&
                mask &&
                (ntohl(((struct sockaddr_in *)mask)->sin_addr.s_addr) == 0L || mask->sa_len == 0))
                cp = "default" ;
            else if (flags & RTF_HOST)
                cp = routename(sin->sin_addr.s_addr);
            else if (mask) {
                cp = netname(sin->sin_addr.s_addr,
                             ntohl(((struct sockaddr_in *)mask)->
                                   sin_addr.s_addr));
            }
            else
                cp = netname(sin->sin_addr.s_addr, 0L);
            break;
        }

//#ifdef INET6
        case AF_INET6: {
            struct sockaddr_in6 *sa6 = (struct sockaddr_in6 *)sa;
            struct in6_addr *in6 = &sa6->sin6_addr;

            /*
             * XXX: This is a special workaround for KAME kernels.
             * sin6_scope_id field of SA should be set in the future.
             */
            if (IN6_IS_ADDR_LINKLOCAL(in6) ||
                IN6_IS_ADDR_MC_NODELOCAL(in6) ||
                IN6_IS_ADDR_MC_LINKLOCAL(in6)) {
                /* XXX: override is ok? */
                sa6->sin6_scope_id = (u_int32_t)ntohs(*(u_short *)&in6->s6_addr[2]);
                *(u_short *)&in6->s6_addr[2] = 0;
            }

            if (flags & RTF_HOST)
                cp = routename6(sa6);
            else if (mask)
                cp = netname6(sa6, mask);
            else
                cp = netname6(sa6, NULL);
            break;
        }
//#endif /*INET6*/

        case AF_LINK: {
            struct sockaddr_dl* sdl = (struct sockaddr_dl*)sa;
            if(sdl->sdl_nlen + sdl->sdl_alen + sdl->sdl_slen == 0)
            {
                (void) snprintf(workbuf, sizeof(workbuf), "link#%d", sdl->sdl_index);
                cp = workbuf;
            } else {
                cp = link_ntoa(sdl);
            }
            break;
        }
            
        default: {
            u_char *s = (u_char *)sa->sa_data, *slim;
            
            slim =  sa->sa_len + (u_char *) sa;
            cplim = cp + sizeof(workbuf) - 6;
            cp += snprintf(cp, sizeof(workbuf) - (cp - workbuf), "(%d)", sa->sa_family);
            while (s < slim && cp < cplim) {
                cp += snprintf(cp, sizeof(workbuf) - (cp - workbuf), " %02x", *s++);
                if (s < slim)
                    cp += snprintf(cp, sizeof(workbuf) - (cp - workbuf), "%02x", *s++);
            }
            cp = workbuf;
        }
    }

    return cp;
}

char *
routename(uint32_t in)
{
	char *cp;
	static char line[MAXHOSTNAMELEN];
	struct hostent *hp;

	cp = 0;
	hp = gethostbyaddr((char *)&in, sizeof (struct in_addr),
		AF_INET);
	if (hp) {
		cp = hp->h_name;
		trimdomain(cp, strlen(cp));
	}

	if (cp) {
		strncpy(line, cp, sizeof(line) - 1);
		line[sizeof(line) - 1] = '\0';
	} else {
#define C(x)	((x) & 0xff)
		in = ntohl(in);
		snprintf(line, sizeof(line), "%u.%u.%u.%u",
		    C(in >> 24), C(in >> 16), C(in >> 8), C(in));
	}
	return (line);
}

char *
routename6(struct sockaddr_in6 *sa6)
{
	static char line[MAXHOSTNAMELEN];
	int flag = NI_WITHSCOPEID;
	/* use local variable for safety */
	struct sockaddr_in6 sa6_local = {sizeof(sa6_local), AF_INET6, };

	sa6_local.sin6_addr = sa6->sin6_addr;
	sa6_local.sin6_scope_id = sa6->sin6_scope_id;

	getnameinfo((struct sockaddr *)&sa6_local, sa6_local.sin6_len,
		    line, sizeof(line), NULL, 0, flag);

	return line;
}
/*
 * Return the name of the network whose address is given.
 * The address is assumed to be that of a net or subnet, not a host.
 */
char *
netname(uint32_t in, uint32_t mask)
{
    char *cp = 0;
    static char line[MAXHOSTNAMELEN];
    struct netent *np = 0;
    uint32_t net, omask, dmask;
    uint32_t i;

    i = ntohl(in);
    dmask = forgemask(i);
    omask = mask;
//    if (!nflag && i) {
    if (i) {
        net = i & dmask;
        if (!(np = getnetbyaddr(i, AF_INET)) && net != i)
            np = getnetbyaddr(net, AF_INET);
        if (np) {
            cp = np->n_name;
            trimdomain(cp, strlen(cp));
        }
    }
    if (cp)
        strncpy(line, cp, sizeof(line) - 1);
    else {
        switch (dmask) {
            case IN_CLASSA_NET:
                if ((i & IN_CLASSA_HOST) == 0) {
                    snprintf(line, sizeof(line), "%u", C(i >> 24));
                    break;
                }
                /* FALLTHROUGH */
            case IN_CLASSB_NET:
                if ((i & IN_CLASSB_HOST) == 0) {
                    snprintf(line, sizeof(line), "%u.%u",
                             C(i >> 24), C(i >> 16));
                    break;
                }
                /* FALLTHROUGH */
            case IN_CLASSC_NET:
                if ((i & IN_CLASSC_HOST) == 0) {
                    snprintf(line, sizeof(line), "%u.%u.%u",
                             C(i >> 24), C(i >> 16), C(i >> 8));
                    break;
                }
                /* FALLTHROUGH */
            default:
                snprintf(line, sizeof(line), "%u.%u.%u.%u",
                         C(i >> 24), C(i >> 16), C(i >> 8), C(i));
                break;
        }
    }
    domask(line+strlen(line), i, omask);
    return (line);
}


char *
netname6(struct sockaddr_in6 *sa6, struct sockaddr *sam)
{
	static char line[MAXHOSTNAMELEN];
	u_char *lim;
	int masklen, illegal = 0, flag = NI_WITHSCOPEID;
	struct in6_addr *mask = sam ? &((struct sockaddr_in6 *)sam)->sin6_addr : 0;

	if (sam && sam->sa_len == 0) {
		masklen = 0;
	} else if (mask) {
		u_char *p = (u_char *)mask;
		for (masklen = 0, lim = p + 16; p < lim; p++) {
			switch (*p) {
			 case 0xff:
				 masklen += 8;
				 break;
			 case 0xfe:
				 masklen += 7;
				 break;
			 case 0xfc:
				 masklen += 6;
				 break;
			 case 0xf8:
				 masklen += 5;
				 break;
			 case 0xf0:
				 masklen += 4;
				 break;
			 case 0xe0:
				 masklen += 3;
				 break;
			 case 0xc0:
				 masklen += 2;
				 break;
			 case 0x80:
				 masklen += 1;
				 break;
			 case 0x00:
				 break;
			 default:
				 illegal ++;
				 break;
			}
		}
		if (illegal)
			fprintf(stderr, "illegal prefixlen\n");
	} else {
		masklen = 128;
	}
	if (masklen == 0 && IN6_IS_ADDR_UNSPECIFIED(&sa6->sin6_addr))
		return("default");

	getnameinfo((struct sockaddr *)sa6, sa6->sin6_len, line, sizeof(line),
		    NULL, 0, flag);

	return line;
}

static uint32_t
forgemask(uint32_t a)
{
	uint32_t m;

	if (IN_CLASSA(a))
		m = IN_CLASSA_NET;
	else if (IN_CLASSB(a))
		m = IN_CLASSB_NET;
	else
		m = IN_CLASSC_NET;
	return (m);
}

static void
domask(char *dst, uint32_t addr, uint32_t mask)
{
	int b, i;

	if (!mask || (forgemask(addr) == mask)) {
		*dst = '\0';
		return;
	}
	i = 0;
	for (b = 0; b < 32; b++)
		if (mask & (1 << b)) {
			int bb;

			i = b;
			for (bb = b+1; bb < 32; bb++)
				if (!(mask & (1 << bb))) {
					i = -1;	/* noncontig */
					break;
				}
			break;
		}
	if (i == -1)
		snprintf(dst, sizeof(dst), "&0x%x", mask);
	else
        snprintf(dst, sizeof(dst), "/%d", 32-i);
}

static void
trimdomain(cp)
char *cp;
{
    static char domain[MAXHOSTNAMELEN + 1];
    static int first = 1;
    char *s;

    if (first) {
        first = 0;
        if (gethostname(domain, MAXHOSTNAMELEN) == 0 &&
            (s = strchr(domain, '.')))
            (void) strcpy(domain, s + 1);
        else
            domain[0] = 0;
    }

    if (domain[0]) {
        while ((cp = strchr(cp, '.'))) {
            if (!strcasecmp(cp + 1, domain)) {
                *cp = 0;        /* hit it */
                break;
            } else {
                cp++;
            }
        }
    }
}

#pragma mark - Getter
- (NSString *)destination {
//    return [self getAddrStringAtIndex:RTAX_DST];
    sa_u dst, netmask;
    bzero(&dst, sizeof(dst));
    if (m_rtm.rtm_addrs & RTA_DST) {
        bcopy(rti_info[RTAX_DST], &dst, rti_info[RTAX_DST]->sa_len);
    }

    bzero(&netmask, sizeof(netmask));
    if (m_rtm.rtm_addrs & RTA_NETMASK) {
        bcopy(rti_info[RTAX_NETMASK], &netmask, rti_info[RTAX_NETMASK]->sa_len);
    }

    char *finalDst = p_sockaddr(&dst.u_sa, &netmask.u_sa, m_rtm.rtm_flags);
    if (finalDst != NULL) {
        return [NSString stringWithCString:finalDst encoding:NSASCIIStringEncoding];
    } else {
        return nil;
    }
}

- (NSString *)gateway {
//    return [self getAddrStringAtIndex:RTAX_GATEWAY];
    sa_u gateway;
    bzero(&gateway, sizeof(gateway));
    if (m_rtm.rtm_addrs & RTA_GATEWAY) {
        bcopy(rti_info[RTAX_GATEWAY], &gateway, rti_info[RTAX_GATEWAY]->sa_len);
    }
    char *finalGateway = p_sockaddr(rti_info[RTAX_GATEWAY], NULL, RTF_HOST);
    if (finalGateway != NULL) {
        return [NSString stringWithCString:finalGateway encoding:NSASCIIStringEncoding];
    } else {
        return nil;
    }
}

- (NSString *)flags {
    NSMutableArray *flagsArray = [NSMutableArray array];
    //https://doc.pfsense.org/index.php/What_do_the_flags_on_the_routing_table_mean
    //https://www.freebsd.org/cgi/man.cgi?query=netstat&sektion=1
    //man netstat to get the relationship

    if (m_rtm.rtm_flags & RTF_UP) {
        [flagsArray addObject:@"U"];
    }
    if (m_rtm.rtm_flags & RTF_GATEWAY) {
        [flagsArray addObject:@"G"];
    }
    if (m_rtm.rtm_flags & RTF_HOST) {
        [flagsArray addObject:@"H"];
    }
    if (m_rtm.rtm_flags & RTF_REJECT) {
        [flagsArray addObject:@"R"];
    }
    if (m_rtm.rtm_flags & RTF_DYNAMIC) {
        [flagsArray addObject:@"D"];
    }
    if (m_rtm.rtm_flags & RTF_MODIFIED) {
        [flagsArray addObject:@"M"];
    }
    if (m_rtm.rtm_flags & RTF_CLONING) {
        [flagsArray addObject:@"C"];
    }
    if (m_rtm.rtm_flags & RTF_XRESOLVE) {
        [flagsArray addObject:@"X"];
    }
    if (m_rtm.rtm_flags & RTF_LLINFO) {
        [flagsArray addObject:@"L"];
    }
    if (m_rtm.rtm_flags & RTF_STATIC) {
        [flagsArray addObject:@"S"];
    }
    if (m_rtm.rtm_flags & RTF_BLACKHOLE) {
        [flagsArray addObject:@"B"];
    }
    if (m_rtm.rtm_flags & RTF_PROTO3) {
        [flagsArray addObject:@"3"];
    }
    if (m_rtm.rtm_flags & RTF_PROTO2) {
        [flagsArray addObject:@"2"];
    }
    if (m_rtm.rtm_flags & RTF_PROTO1) {
        [flagsArray addObject:@"1"];
    }
    if (m_rtm.rtm_flags & RTF_PRCLONING) {
        [flagsArray addObject:@"c"];
    }
    if (m_rtm.rtm_flags & RTF_WASCLONED) {
        [flagsArray addObject:@"W"];
    }
    if (m_rtm.rtm_flags & RTF_BROADCAST) {
        [flagsArray addObject:@"b"];
    }
    if (m_rtm.rtm_flags & RTF_IFSCOPE) {
        [flagsArray addObject:@"I"];
    }
    if (m_rtm.rtm_flags & RTF_IFREF) {
        [flagsArray addObject:@"i"];
    }
    if (m_rtm.rtm_flags & RTF_MULTICAST) {
        [flagsArray addObject:@"m"];
    }
    if (m_rtm.rtm_flags & RTF_ROUTER) {
        [flagsArray addObject:@"r"];
    }
    if (m_rtm.rtm_flags & RTF_PROXY) {
        [flagsArray addObject:@"Y"];
    }

    return [flagsArray componentsJoinedByString:@""];
}

- (NSInteger)refs {
    return m_rtm.rtm_refcnt;
}

- (NSInteger)use {
    return m_rtm.rtm_use;
}

- (NSString *)netif {
    char ifName[IF_NAMESIZE];
    char *name = if_indextoname(m_rtm.rtm_index, ifName);
    if (name != NULL) {
        return [NSString stringWithCString:name encoding:NSASCIIStringEncoding];
    } else {
        return nil;
    }
}

- (NSInteger)expire {
    time_t expire_time = m_rtm.rtm_rmx.rmx_expire - time((time_t *)0);
    if (expire_time > 0) {
        return [[NSString stringWithFormat:@"%6ld", expire_time] integerValue];
    } else {
        return 0;
    }
}

- (NSInteger)mtu {
    return m_rtm.rtm_rmx.rmx_mtu;
}
@end
