//
//  RouteRecord.h
//
//  Created by jianpx on 8/26/16.
//  Copyright © 2016 . All rights reserved.
//

//http://www.masterraghu.com/subjects/np/introduction/unix_network_programming_v1.3/ch18lev1sec3.html

#import <Foundation/Foundation.h>

#if TARGET_IPHONE_SIMULATOR
#include <net/route.h>
#else
#include "route.h"
#endif


@interface RouteRecord : NSObject
@property (copy, nonatomic) NSString *destination;
@property (copy, nonatomic) NSString *gateway;
@property (copy, nonatomic) NSString *flags;
@property (assign, nonatomic) NSInteger refs;
@property (assign, nonatomic) NSInteger mtu;
@property (assign, nonatomic) NSInteger use;
@property (copy, nonatomic) NSString *netif;
@property (assign, nonatomic) NSInteger expire;

- (instancetype)initWithRtm:(struct rt_msghdr2 *)rtm;
@end
