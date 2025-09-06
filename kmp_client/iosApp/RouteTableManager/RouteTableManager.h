//
//  RouteTableManager.h
//
//  Created by jianpx on 8/26/16.
//  Copyright © 2016 . All rights reserved.
//

//http://www.masterraghu.com/subjects/np/introduction/unix_network_programming_v1.3/ch18lev1sec3.html

#import <Foundation/Foundation.h>
#import "RouteRecord.h"

@interface RouteTableManager : NSObject

+ (NSArray<RouteRecord *> *)getAllRoutes;
+ (NSString *)formatRouteTable;
@end
