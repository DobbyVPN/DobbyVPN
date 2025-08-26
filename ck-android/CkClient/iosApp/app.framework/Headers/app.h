#import <Foundation/NSArray.h>
#import <Foundation/NSDictionary.h>
#import <Foundation/NSError.h>
#import <Foundation/NSObject.h>
#import <Foundation/NSSet.h>
#import <Foundation/NSString.h>
#import <Foundation/NSValue.h>

@class AppIpData, AppKotlinArray<T>, AppLifecycle_viewmodelViewModel, AppIpData_Companion, AppIpData_, AppUiDataCompanion, AppUiData, AppLogsRepository, AppOkioPath, AppLogsUiState, AppVpnInterface, AppKotlinEnumCompanion, AppKotlinEnum<E>, AppVpnInterfaceCompanion, AppAwgConnectionState, AppConnectionStateRepository, AppPermissionEventsChannel, AppMainUiState, AppKotlinByteArray, AppAboutScreen, AppAmneziaWGScreen, AppDiagnosticsScreen, AppLogsScreen, AppMainScreen, AppSettingsScreen, AppMainViewModel, AppOkioFileSystem, UIViewController, AppKoin_coreModule, AppKoin_coreScope, AppKoin_coreKoinApplication, AppOkioPathCompanion, AppOkioByteString, AppKotlinThrowable, AppKotlinException, AppKotlinRuntimeException, AppKotlinIllegalStateException, AppKotlinByteIterator, AppOkioFileSystemCompanion, AppOkioFileMetadata, AppOkioFileHandle, AppKoin_coreKoinDefinition<R>, AppKoin_coreParametersHolder, AppKoin_coreInstanceFactory<T>, AppKoin_coreSingleInstanceFactory<T>, AppKoin_coreScopeDSL, AppKoin_coreLockable, AppKoin_coreKoin, AppKotlinLazyThreadSafetyMode, AppStately_concurrencyThreadLocalRef<T>, AppKoin_coreLogger, AppKoin_coreKoinApplicationCompanion, AppKoin_coreLevel, AppOkioByteStringCompanion, AppKotlinx_serialization_coreSerializersModule, AppKotlinx_serialization_coreSerialKind, AppKotlinNothing, AppOkioTimeout, AppOkioBuffer, AppOkioLock, AppKoin_coreBeanDefinition<T>, AppKoin_coreInstanceFactoryCompanion, AppKoin_coreInstanceContext, AppKoin_coreExtensionManager, AppKoin_coreInstanceRegistry, AppKoin_corePropertyRegistry, AppKoin_coreScopeRegistry, NSData, AppOkioTimeoutCompanion, AppOkioBufferUnsafeCursor, AppOkioLockCompanion, AppKoin_coreKind, AppKoin_coreCallbacks<T>, AppKoin_coreScopeRegistryCompanion;

@protocol AppKotlinAutoCloseable, AppKotlinx_coroutines_coreCoroutineScope, AppIpRepository, AppRuntimeState, AppKotlinx_coroutines_coreStateFlow, AppCopyLogsInteractor, AppKotlinx_coroutines_coreSharedFlow, AppKotlinComparable, AppDobbyConfigsRepository, AppVpnManager, AppAwgManager, AppRuntimeMutableState, AppKotlinx_serialization_coreKSerializer, AppKotlinIterator, AppKotlinCoroutineContext, AppKotlinx_coroutines_coreFlowCollector, AppKotlinx_coroutines_coreFlow, AppKotlinx_serialization_coreEncoder, AppKotlinx_serialization_coreSerialDescriptor, AppKotlinx_serialization_coreSerializationStrategy, AppKotlinx_serialization_coreDecoder, AppKotlinx_serialization_coreDeserializationStrategy, AppOkioSink, AppKotlinSequence, AppOkioBufferedSource, AppOkioSource, AppOkioBufferedSink, AppKoin_coreQualifier, AppKotlinKClass, AppKotlinLazy, AppKoin_coreScopeCallback, AppKotlinCoroutineContextElement, AppKotlinCoroutineContextKey, AppKotlinx_serialization_coreCompositeEncoder, AppKotlinAnnotation, AppKotlinx_serialization_coreCompositeDecoder, AppOkioCloseable, AppKoin_coreKoinScopeComponent, AppKotlinKDeclarationContainer, AppKotlinKAnnotatedElement, AppKotlinKClassifier, AppKotlinx_serialization_coreSerializersModuleCollector, AppKoin_coreKoinComponent, AppKoin_coreKoinExtension;

NS_ASSUME_NONNULL_BEGIN
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wunknown-warning-option"
#pragma clang diagnostic ignored "-Wincompatible-property-type"
#pragma clang diagnostic ignored "-Wnullability"

#pragma push_macro("_Nullable_result")
#if !__has_feature(nullability_nullable_result)
#undef _Nullable_result
#define _Nullable_result _Nullable
#endif

__attribute__((swift_name("KotlinBase")))
@interface AppBase : NSObject
- (instancetype)init __attribute__((unavailable));
+ (instancetype)new __attribute__((unavailable));
+ (void)initialize __attribute__((objc_requires_super));
@end

@interface AppBase (AppBaseCopying) <NSCopying>
@end

__attribute__((swift_name("KotlinMutableSet")))
@interface AppMutableSet<ObjectType> : NSMutableSet<ObjectType>
@end

__attribute__((swift_name("KotlinMutableDictionary")))
@interface AppMutableDictionary<KeyType, ObjectType> : NSMutableDictionary<KeyType, ObjectType>
@end

@interface NSError (NSErrorAppKotlinException)
@property (readonly) id _Nullable kotlinException;
@end

__attribute__((swift_name("KotlinNumber")))
@interface AppNumber : NSNumber
- (instancetype)initWithChar:(char)value __attribute__((unavailable));
- (instancetype)initWithUnsignedChar:(unsigned char)value __attribute__((unavailable));
- (instancetype)initWithShort:(short)value __attribute__((unavailable));
- (instancetype)initWithUnsignedShort:(unsigned short)value __attribute__((unavailable));
- (instancetype)initWithInt:(int)value __attribute__((unavailable));
- (instancetype)initWithUnsignedInt:(unsigned int)value __attribute__((unavailable));
- (instancetype)initWithLong:(long)value __attribute__((unavailable));
- (instancetype)initWithUnsignedLong:(unsigned long)value __attribute__((unavailable));
- (instancetype)initWithLongLong:(long long)value __attribute__((unavailable));
- (instancetype)initWithUnsignedLongLong:(unsigned long long)value __attribute__((unavailable));
- (instancetype)initWithFloat:(float)value __attribute__((unavailable));
- (instancetype)initWithDouble:(double)value __attribute__((unavailable));
- (instancetype)initWithBool:(BOOL)value __attribute__((unavailable));
- (instancetype)initWithInteger:(NSInteger)value __attribute__((unavailable));
- (instancetype)initWithUnsignedInteger:(NSUInteger)value __attribute__((unavailable));
+ (instancetype)numberWithChar:(char)value __attribute__((unavailable));
+ (instancetype)numberWithUnsignedChar:(unsigned char)value __attribute__((unavailable));
+ (instancetype)numberWithShort:(short)value __attribute__((unavailable));
+ (instancetype)numberWithUnsignedShort:(unsigned short)value __attribute__((unavailable));
+ (instancetype)numberWithInt:(int)value __attribute__((unavailable));
+ (instancetype)numberWithUnsignedInt:(unsigned int)value __attribute__((unavailable));
+ (instancetype)numberWithLong:(long)value __attribute__((unavailable));
+ (instancetype)numberWithUnsignedLong:(unsigned long)value __attribute__((unavailable));
+ (instancetype)numberWithLongLong:(long long)value __attribute__((unavailable));
+ (instancetype)numberWithUnsignedLongLong:(unsigned long long)value __attribute__((unavailable));
+ (instancetype)numberWithFloat:(float)value __attribute__((unavailable));
+ (instancetype)numberWithDouble:(double)value __attribute__((unavailable));
+ (instancetype)numberWithBool:(BOOL)value __attribute__((unavailable));
+ (instancetype)numberWithInteger:(NSInteger)value __attribute__((unavailable));
+ (instancetype)numberWithUnsignedInteger:(NSUInteger)value __attribute__((unavailable));
@end

__attribute__((swift_name("KotlinByte")))
@interface AppByte : AppNumber
- (instancetype)initWithChar:(char)value;
+ (instancetype)numberWithChar:(char)value;
@end

__attribute__((swift_name("KotlinUByte")))
@interface AppUByte : AppNumber
- (instancetype)initWithUnsignedChar:(unsigned char)value;
+ (instancetype)numberWithUnsignedChar:(unsigned char)value;
@end

__attribute__((swift_name("KotlinShort")))
@interface AppShort : AppNumber
- (instancetype)initWithShort:(short)value;
+ (instancetype)numberWithShort:(short)value;
@end

__attribute__((swift_name("KotlinUShort")))
@interface AppUShort : AppNumber
- (instancetype)initWithUnsignedShort:(unsigned short)value;
+ (instancetype)numberWithUnsignedShort:(unsigned short)value;
@end

__attribute__((swift_name("KotlinInt")))
@interface AppInt : AppNumber
- (instancetype)initWithInt:(int)value;
+ (instancetype)numberWithInt:(int)value;
@end

__attribute__((swift_name("KotlinUInt")))
@interface AppUInt : AppNumber
- (instancetype)initWithUnsignedInt:(unsigned int)value;
+ (instancetype)numberWithUnsignedInt:(unsigned int)value;
@end

__attribute__((swift_name("KotlinLong")))
@interface AppLong : AppNumber
- (instancetype)initWithLongLong:(long long)value;
+ (instancetype)numberWithLongLong:(long long)value;
@end

__attribute__((swift_name("KotlinULong")))
@interface AppULong : AppNumber
- (instancetype)initWithUnsignedLongLong:(unsigned long long)value;
+ (instancetype)numberWithUnsignedLongLong:(unsigned long long)value;
@end

__attribute__((swift_name("KotlinFloat")))
@interface AppFloat : AppNumber
- (instancetype)initWithFloat:(float)value;
+ (instancetype)numberWithFloat:(float)value;
@end

__attribute__((swift_name("KotlinDouble")))
@interface AppDouble : AppNumber
- (instancetype)initWithDouble:(double)value;
+ (instancetype)numberWithDouble:(double)value;
@end

__attribute__((swift_name("KotlinBoolean")))
@interface AppBoolean : AppNumber
- (instancetype)initWithBool:(BOOL)value;
+ (instancetype)numberWithBool:(BOOL)value;
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("IpData")))
@interface AppIpData : AppBase
- (instancetype)initWithIp:(NSString *)ip city:(NSString *)city country:(NSString *)country __attribute__((swift_name("init(ip:city:country:)"))) __attribute__((objc_designated_initializer));
- (AppIpData *)doCopyIp:(NSString *)ip city:(NSString *)city country:(NSString *)country __attribute__((swift_name("doCopy(ip:city:country:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) NSString *city __attribute__((swift_name("city")));
@property (readonly) NSString *country __attribute__((swift_name("country")));
@property (readonly) NSString *ip __attribute__((swift_name("ip")));
@end

__attribute__((swift_name("IpRepository")))
@protocol AppIpRepository
@required
- (AppIpData *)getHostnameIpDataHostname:(NSString *)hostname __attribute__((swift_name("getHostnameIpData(hostname:)")));
- (AppIpData *)getIpData __attribute__((swift_name("getIpData()")));
@end

__attribute__((swift_name("Lifecycle_viewmodelViewModel")))
@interface AppLifecycle_viewmodelViewModel : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (instancetype)initWithCloseables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(closeables:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope __attribute__((swift_name("init(viewModelScope:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope closeables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(viewModelScope:closeables:)"))) __attribute__((objc_designated_initializer));
- (void)addCloseableCloseable:(id<AppKotlinAutoCloseable>)closeable __attribute__((swift_name("addCloseable(closeable:)")));
- (void)addCloseableKey:(NSString *)key closeable:(id<AppKotlinAutoCloseable>)closeable __attribute__((swift_name("addCloseable(key:closeable:)")));
- (id<AppKotlinAutoCloseable> _Nullable)getCloseableKey:(NSString *)key __attribute__((swift_name("getCloseable(key:)")));

/**
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (void)onCleared __attribute__((swift_name("onCleared()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("DiagnosticViewModel")))
@interface AppDiagnosticViewModel : AppLifecycle_viewmodelViewModel
- (instancetype)initWithIpRepository:(id<AppIpRepository>)ipRepository __attribute__((swift_name("init(ipRepository:)"))) __attribute__((objc_designated_initializer));
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
+ (instancetype)new __attribute__((unavailable));
- (instancetype)initWithCloseables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(closeables:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope __attribute__((swift_name("init(viewModelScope:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope closeables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(viewModelScope:closeables:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (void)reloadDnsIpDataHostname:(NSString *)hostname __attribute__((swift_name("reloadDnsIpData(hostname:)")));
- (void)reloadIpData __attribute__((swift_name("reloadIpData()")));
@property (readonly) id<AppRuntimeState> uiState __attribute__((swift_name("uiState")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("IpData_")))
@interface AppIpData_ : AppBase
- (instancetype)initWithIp:(NSString *)ip city:(NSString *)city country:(NSString *)country __attribute__((swift_name("init(ip:city:country:)"))) __attribute__((objc_designated_initializer));
@property (class, readonly, getter=companion) AppIpData_Companion *companion __attribute__((swift_name("companion")));
- (AppIpData_ *)doCopyIp:(NSString *)ip city:(NSString *)city country:(NSString *)country __attribute__((swift_name("doCopy(ip:city:country:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) NSString *city __attribute__((swift_name("city")));
@property (readonly) NSString *country __attribute__((swift_name("country")));
@property (readonly) NSString *ip __attribute__((swift_name("ip")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("IpData_.Companion")))
@interface AppIpData_Companion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppIpData_Companion *shared __attribute__((swift_name("shared")));
@property (readonly) AppIpData_ *EMPTY __attribute__((swift_name("EMPTY")));
@property (readonly) AppIpData_ *LOADING __attribute__((swift_name("LOADING")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("UiData")))
@interface AppUiData : AppBase
- (instancetype)initWithIpData:(AppIpData_ *)ipData dnsData:(AppIpData_ *)dnsData __attribute__((swift_name("init(ipData:dnsData:)"))) __attribute__((objc_designated_initializer));
@property (class, readonly, getter=companion) AppUiDataCompanion *companion __attribute__((swift_name("companion")));
- (AppUiData *)doCopyIpData:(AppIpData_ *)ipData dnsData:(AppIpData_ *)dnsData __attribute__((swift_name("doCopy(ipData:dnsData:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property AppIpData_ *dnsData __attribute__((swift_name("dnsData")));
@property AppIpData_ *ipData __attribute__((swift_name("ipData")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("UiData.Companion")))
@interface AppUiDataCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppUiDataCompanion *shared __attribute__((swift_name("shared")));
@property (readonly) AppUiData *EMPTY __attribute__((swift_name("EMPTY")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Logger")))
@interface AppLogger : AppBase
- (instancetype)initWithLogsRepository:(AppLogsRepository *)logsRepository __attribute__((swift_name("init(logsRepository:)"))) __attribute__((objc_designated_initializer));
- (void)logMessage:(NSString *)message __attribute__((swift_name("log(message:)")));
@end

__attribute__((swift_name("CopyLogsInteractor")))
@protocol AppCopyLogsInteractor
@required
- (void)doCopyLogs:(NSArray<NSString *> *)logs __attribute__((swift_name("doCopy(logs:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("LogsRepository")))
@interface AppLogsRepository : AppBase
- (instancetype)initWithLogFilePath:(AppOkioPath *)logFilePath __attribute__((swift_name("init(logFilePath:)"))) __attribute__((objc_designated_initializer));
- (void)clearLogs __attribute__((swift_name("clearLogs()")));
- (void)writeLogLog:(NSString *)log __attribute__((swift_name("writeLog(log:)")));
@property (readonly) id<AppKotlinx_coroutines_coreStateFlow> logState __attribute__((swift_name("logState")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("LogsViewModel")))
@interface AppLogsViewModel : AppLifecycle_viewmodelViewModel
- (instancetype)initWithLogsRepository:(AppLogsRepository *)logsRepository copyLogsInteractor:(id<AppCopyLogsInteractor>)copyLogsInteractor __attribute__((swift_name("init(logsRepository:copyLogsInteractor:)"))) __attribute__((objc_designated_initializer));
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
+ (instancetype)new __attribute__((unavailable));
- (instancetype)initWithCloseables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(closeables:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope __attribute__((swift_name("init(viewModelScope:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope closeables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(viewModelScope:closeables:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (void)clearLogs __attribute__((swift_name("clearLogs()")));
- (void)doCopyLogsToClipBoard __attribute__((swift_name("doCopyLogsToClipBoard()")));
- (void)dispose __attribute__((swift_name("dispose()")));

/**
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (void)onCleared __attribute__((swift_name("onCleared()")));
@property (readonly) id<AppKotlinx_coroutines_coreStateFlow> uiState __attribute__((swift_name("uiState")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("LogsUiState")))
@interface AppLogsUiState : AppBase
- (instancetype)initWithLogMessages:(NSArray<NSString *> *)logMessages __attribute__((swift_name("init(logMessages:)"))) __attribute__((objc_designated_initializer));
- (AppLogsUiState *)doCopyLogMessages:(NSArray<NSString *> *)logMessages __attribute__((swift_name("doCopy(logMessages:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) NSArray<NSString *> *logMessages __attribute__((swift_name("logMessages")));
@end

__attribute__((swift_name("AwgManager")))
@protocol AppAwgManager
@required
- (NSString *)getAwgVersion __attribute__((swift_name("getAwgVersion()")));
- (void)onAwgConnect __attribute__((swift_name("onAwgConnect()")));
- (void)onAwgDisconnect __attribute__((swift_name("onAwgDisconnect()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("ConnectionStateRepository")))
@interface AppConnectionStateRepository : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (void)tryUpdateIsConnected:(BOOL)isConnected __attribute__((swift_name("tryUpdate(isConnected:)")));

/**
 * @note This method converts instances of CancellationException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (void)updateIsConnected:(BOOL)isConnected completionHandler:(void (^)(NSError * _Nullable))completionHandler __attribute__((swift_name("update(isConnected:completionHandler:)")));
@property (readonly) id<AppKotlinx_coroutines_coreStateFlow> flow __attribute__((swift_name("flow")));
@end

__attribute__((swift_name("DobbyConfigsRepository")))
@protocol AppDobbyConfigsRepository
@required
- (NSString *)getAwgConfig __attribute__((swift_name("getAwgConfig()")));
- (NSString *)getCloakConfig __attribute__((swift_name("getCloakConfig()")));
- (BOOL)getIsAmneziaWGEnabled __attribute__((swift_name("getIsAmneziaWGEnabled()")));
- (BOOL)getIsCloakEnabled __attribute__((swift_name("getIsCloakEnabled()")));
- (BOOL)getIsOutlineEnabled __attribute__((swift_name("getIsOutlineEnabled()")));
- (NSString *)getOutlineKey __attribute__((swift_name("getOutlineKey()")));
- (AppVpnInterface *)getVpnInterface __attribute__((swift_name("getVpnInterface()")));
- (void)setAwgConfigNewConfig:(NSString *)newConfig __attribute__((swift_name("setAwgConfig(newConfig:)")));
- (void)setCloakConfigNewConfig:(NSString *)newConfig __attribute__((swift_name("setCloakConfig(newConfig:)")));
- (void)setIsAmneziaWGEnabledIsAmneziaWGEnabled:(BOOL)isAmneziaWGEnabled __attribute__((swift_name("setIsAmneziaWGEnabled(isAmneziaWGEnabled:)")));
- (void)setIsCloakEnabledIsCloakEnabled:(BOOL)isCloakEnabled __attribute__((swift_name("setIsCloakEnabled(isCloakEnabled:)")));
- (void)setIsOutlineEnabledIsOutlineEnabled:(BOOL)isOutlineEnabled __attribute__((swift_name("setIsOutlineEnabled(isOutlineEnabled:)")));
- (void)setOutlineKeyNewOutlineKey:(NSString *)newOutlineKey __attribute__((swift_name("setOutlineKey(newOutlineKey:)")));
- (void)setVpnInterfaceVpnInterface:(AppVpnInterface *)vpnInterface __attribute__((swift_name("setVpnInterface(vpnInterface:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("PermissionEventsChannel")))
@interface AppPermissionEventsChannel : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));

/**
 * @note This method converts instances of CancellationException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (void)checkPermissionsWithCompletionHandler:(void (^)(NSError * _Nullable))completionHandler __attribute__((swift_name("checkPermissions(completionHandler:)")));

/**
 * @note This method converts instances of CancellationException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (void)onPermissionGrantedIsGranted:(BOOL)isGranted completionHandler:(void (^)(NSError * _Nullable))completionHandler __attribute__((swift_name("onPermissionGranted(isGranted:completionHandler:)")));
@property (readonly) id<AppKotlinx_coroutines_coreSharedFlow> checkPermissionsEvents __attribute__((swift_name("checkPermissionsEvents")));
@property (readonly) id<AppKotlinx_coroutines_coreSharedFlow> permissionsGrantedEvents __attribute__((swift_name("permissionsGrantedEvents")));
@end

__attribute__((swift_name("KotlinComparable")))
@protocol AppKotlinComparable
@required
- (int32_t)compareToOther:(id _Nullable)other __attribute__((swift_name("compareTo(other:)")));
@end

__attribute__((swift_name("KotlinEnum")))
@interface AppKotlinEnum<E> : AppBase <AppKotlinComparable>
- (instancetype)initWithName:(NSString *)name ordinal:(int32_t)ordinal __attribute__((swift_name("init(name:ordinal:)"))) __attribute__((objc_designated_initializer));
@property (class, readonly, getter=companion) AppKotlinEnumCompanion *companion __attribute__((swift_name("companion")));
- (int32_t)compareToOther:(E)other __attribute__((swift_name("compareTo(other:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) NSString *name __attribute__((swift_name("name")));
@property (readonly) int32_t ordinal __attribute__((swift_name("ordinal")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("VpnInterface")))
@interface AppVpnInterface : AppKotlinEnum<AppVpnInterface *>
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
- (instancetype)initWithName:(NSString *)name ordinal:(int32_t)ordinal __attribute__((swift_name("init(name:ordinal:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
@property (class, readonly, getter=companion) AppVpnInterfaceCompanion *companion __attribute__((swift_name("companion")));
@property (class, readonly) AppVpnInterface *cloakOutline __attribute__((swift_name("cloakOutline")));
@property (class, readonly) AppVpnInterface *amneziaWg __attribute__((swift_name("amneziaWg")));
+ (AppKotlinArray<AppVpnInterface *> *)values __attribute__((swift_name("values()")));
@property (class, readonly) NSArray<AppVpnInterface *> *entries __attribute__((swift_name("entries")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("VpnInterface.Companion")))
@interface AppVpnInterfaceCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppVpnInterfaceCompanion *shared __attribute__((swift_name("shared")));
@property (readonly) AppVpnInterface *DEFAULT_VALUE __attribute__((swift_name("DEFAULT_VALUE")));
@end

__attribute__((swift_name("VpnManager")))
@protocol AppVpnManager
@required
- (void)start __attribute__((swift_name("start()")));
- (void)stop __attribute__((swift_name("stop()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("AwgConnectionState")))
@interface AppAwgConnectionState : AppKotlinEnum<AppAwgConnectionState *>
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
- (instancetype)initWithName:(NSString *)name ordinal:(int32_t)ordinal __attribute__((swift_name("init(name:ordinal:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
@property (class, readonly) AppAwgConnectionState *on __attribute__((swift_name("on")));
@property (class, readonly) AppAwgConnectionState *off __attribute__((swift_name("off")));
+ (AppKotlinArray<AppAwgConnectionState *> *)values __attribute__((swift_name("values()")));
@property (class, readonly) NSArray<AppAwgConnectionState *> *entries __attribute__((swift_name("entries")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("MainViewModel")))
@interface AppMainViewModel : AppLifecycle_viewmodelViewModel
- (instancetype)initWithConfigsRepository:(id<AppDobbyConfigsRepository>)configsRepository connectionStateRepository:(AppConnectionStateRepository *)connectionStateRepository permissionEventsChannel:(AppPermissionEventsChannel *)permissionEventsChannel vpnManager:(id<AppVpnManager>)vpnManager awgManager:(id<AppAwgManager>)awgManager __attribute__((swift_name("init(configsRepository:connectionStateRepository:permissionEventsChannel:vpnManager:awgManager:)"))) __attribute__((objc_designated_initializer));
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
+ (instancetype)new __attribute__((unavailable));
- (instancetype)initWithCloseables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(closeables:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope __attribute__((swift_name("init(viewModelScope:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (instancetype)initWithViewModelScope:(id<AppKotlinx_coroutines_coreCoroutineScope>)viewModelScope closeables:(AppKotlinArray<id<AppKotlinAutoCloseable>> *)closeables __attribute__((swift_name("init(viewModelScope:closeables:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
- (void)onAwgConfigEditNewConfig:(NSString *)newConfig __attribute__((swift_name("onAwgConfigEdit(newConfig:)")));
- (void)onAwgConnect __attribute__((swift_name("onAwgConnect()")));
- (void)onAwgDisconnect __attribute__((swift_name("onAwgDisconnect()")));
- (void)onConnectionButtonClickedCloakJson:(NSString * _Nullable)cloakJson outlineKey:(NSString *)outlineKey isCloakEnabled:(BOOL)isCloakEnabled __attribute__((swift_name("onConnectionButtonClicked(cloakJson:outlineKey:isCloakEnabled:)")));
@property (readonly) id<AppRuntimeMutableState> awgConfigState __attribute__((swift_name("awgConfigState")));
@property (readonly) id<AppRuntimeMutableState> awgConnectionState __attribute__((swift_name("awgConnectionState")));
@property (readonly) NSString *awgVersion __attribute__((swift_name("awgVersion")));
@property (readonly) id<AppKotlinx_coroutines_coreStateFlow> uiState __attribute__((swift_name("uiState")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("MainUiState")))
@interface AppMainUiState : AppBase
- (instancetype)initWithCloakJson:(NSString *)cloakJson outlineKey:(NSString *)outlineKey isConnected:(BOOL)isConnected isCloakEnabled:(BOOL)isCloakEnabled __attribute__((swift_name("init(cloakJson:outlineKey:isConnected:isCloakEnabled:)"))) __attribute__((objc_designated_initializer));
- (AppMainUiState *)doCopyCloakJson:(NSString *)cloakJson outlineKey:(NSString *)outlineKey isConnected:(BOOL)isConnected isCloakEnabled:(BOOL)isCloakEnabled __attribute__((swift_name("doCopy(cloakJson:outlineKey:isConnected:isCloakEnabled:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) NSString *cloakJson __attribute__((swift_name("cloakJson")));
@property (readonly) BOOL isCloakEnabled __attribute__((swift_name("isCloakEnabled")));
@property (readonly) BOOL isConnected __attribute__((swift_name("isConnected")));
@property (readonly) NSString *outlineKey __attribute__((swift_name("outlineKey")));
@end

__attribute__((swift_name("CloakLibFacade")))
@protocol AppCloakLibFacade
@required
- (void)restartClient __attribute__((swift_name("restartClient()")));
- (void)startClientLocalHost:(NSString *)localHost localPort:(NSString *)localPort config:(NSString *)config __attribute__((swift_name("startClient(localHost:localPort:config:)")));
- (void)stopClient __attribute__((swift_name("stopClient()")));
@end

__attribute__((swift_name("OutlineLibFacade")))
@protocol AppOutlineLibFacade
@required
- (void)disconnect __attribute__((swift_name("disconnect()")));
- (void)doInitApiKey:(NSString *)apiKey __attribute__((swift_name("doInit(apiKey:)")));
- (int32_t)readDataData:(AppKotlinByteArray *)data __attribute__((swift_name("readData(data:)")));
- (void)writeDataData:(AppKotlinByteArray *)data length:(int32_t)length __attribute__((swift_name("writeData(data:length:)")));
@end


/**
 * @note annotations
 *   kotlinx.serialization.Serializable
*/
__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("AboutScreen")))
@interface AppAboutScreen : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)aboutScreen __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppAboutScreen *shared __attribute__((swift_name("shared")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializer __attribute__((swift_name("serializer()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializerTypeParamsSerializers:(AppKotlinArray<id<AppKotlinx_serialization_coreKSerializer>> *)typeParamsSerializers __attribute__((swift_name("serializer(typeParamsSerializers:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@end


/**
 * @note annotations
 *   kotlinx.serialization.Serializable
*/
__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("AmneziaWGScreen")))
@interface AppAmneziaWGScreen : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)amneziaWGScreen __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppAmneziaWGScreen *shared __attribute__((swift_name("shared")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializer __attribute__((swift_name("serializer()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializerTypeParamsSerializers:(AppKotlinArray<id<AppKotlinx_serialization_coreKSerializer>> *)typeParamsSerializers __attribute__((swift_name("serializer(typeParamsSerializers:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@end


/**
 * @note annotations
 *   kotlinx.serialization.Serializable
*/
__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("DiagnosticsScreen")))
@interface AppDiagnosticsScreen : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)diagnosticsScreen __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppDiagnosticsScreen *shared __attribute__((swift_name("shared")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializer __attribute__((swift_name("serializer()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializerTypeParamsSerializers:(AppKotlinArray<id<AppKotlinx_serialization_coreKSerializer>> *)typeParamsSerializers __attribute__((swift_name("serializer(typeParamsSerializers:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@end


/**
 * @note annotations
 *   kotlinx.serialization.Serializable
*/
__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("LogsScreen")))
@interface AppLogsScreen : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)logsScreen __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppLogsScreen *shared __attribute__((swift_name("shared")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializer __attribute__((swift_name("serializer()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializerTypeParamsSerializers:(AppKotlinArray<id<AppKotlinx_serialization_coreKSerializer>> *)typeParamsSerializers __attribute__((swift_name("serializer(typeParamsSerializers:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@end


/**
 * @note annotations
 *   kotlinx.serialization.Serializable
*/
__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("MainScreen")))
@interface AppMainScreen : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)mainScreen __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppMainScreen *shared __attribute__((swift_name("shared")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializer __attribute__((swift_name("serializer()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializerTypeParamsSerializers:(AppKotlinArray<id<AppKotlinx_serialization_coreKSerializer>> *)typeParamsSerializers __attribute__((swift_name("serializer(typeParamsSerializers:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@end


/**
 * @note annotations
 *   kotlinx.serialization.Serializable
*/
__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("SettingsScreen")))
@interface AppSettingsScreen : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)settingsScreen __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppSettingsScreen *shared __attribute__((swift_name("shared")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializer __attribute__((swift_name("serializer()")));
- (id<AppKotlinx_serialization_coreKSerializer>)serializerTypeParamsSerializers:(AppKotlinArray<id<AppKotlinx_serialization_coreKSerializer>> *)typeParamsSerializers __attribute__((swift_name("serializer(typeParamsSerializers:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("IsPermissionCheckNeeded_iosKt")))
@interface AppIsPermissionCheckNeeded_iosKt : AppBase
@property (class, readonly) BOOL isPermissionCheckNeeded __attribute__((swift_name("isPermissionCheckNeeded")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("KoinKt")))
@interface AppKoinKt : AppBase
+ (AppMainViewModel *)getMainViewModel __attribute__((swift_name("getMainViewModel()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("LogsRepository_iosKt")))
@interface AppLogsRepository_iosKt : AppBase
+ (AppOkioPath *)provideLogFilePath __attribute__((swift_name("provideLogFilePath()")));
@property (class, readonly) AppOkioFileSystem *fileSystem __attribute__((swift_name("fileSystem")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("MainViewControllerKt")))
@interface AppMainViewControllerKt : AppBase
+ (UIViewController *)MainViewController __attribute__((swift_name("MainViewController()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("MakeNativeModuleKt")))
@interface AppMakeNativeModuleKt : AppBase
+ (AppKoin_coreModule *)makeNativeModuleCopyLogsInteractor:(id<AppCopyLogsInteractor> (^)(AppKoin_coreScope *))copyLogsInteractor logsRepository:(AppLogsRepository *(^)(AppKoin_coreScope *))logsRepository ipRepository:(id<AppIpRepository> (^)(AppKoin_coreScope *))ipRepository configsRepository:(id<AppDobbyConfigsRepository> (^)(AppKoin_coreScope *))configsRepository connectionStateRepository:(AppConnectionStateRepository *(^)(AppKoin_coreScope *))connectionStateRepository vpnManager:(id<AppVpnManager> (^)(AppKoin_coreScope *))vpnManager awgManager:(id<AppAwgManager> (^)(AppKoin_coreScope *))awgManager __attribute__((swift_name("makeNativeModule(copyLogsInteractor:logsRepository:ipRepository:configsRepository:connectionStateRepository:vpnManager:awgManager:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("StartDIKt")))
@interface AppStartDIKt : AppBase
+ (void)startDINativeModules:(NSArray<AppKoin_coreModule *> *)nativeModules appDeclaration:(void (^)(AppKoin_coreKoinApplication *))appDeclaration __attribute__((swift_name("startDI(nativeModules:appDeclaration:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("ViewModelModuleKt")))
@interface AppViewModelModuleKt : AppBase
@property (class, readonly) AppKoin_coreModule *sharedModule __attribute__((swift_name("sharedModule")));
@end


/**
 * @note annotations
 *   kotlin.SinceKotlin(version="2.0")
*/
__attribute__((swift_name("KotlinAutoCloseable")))
@protocol AppKotlinAutoCloseable
@required
- (void)close __attribute__((swift_name("close()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("KotlinArray")))
@interface AppKotlinArray<T> : AppBase
+ (instancetype)arrayWithSize:(int32_t)size init:(T _Nullable (^)(AppInt *))init __attribute__((swift_name("init(size:init:)")));
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
- (T _Nullable)getIndex:(int32_t)index __attribute__((swift_name("get(index:)")));
- (id<AppKotlinIterator>)iterator __attribute__((swift_name("iterator()")));
- (void)setIndex:(int32_t)index value:(T _Nullable)value __attribute__((swift_name("set(index:value:)")));
@property (readonly) int32_t size __attribute__((swift_name("size")));
@end

__attribute__((swift_name("Kotlinx_coroutines_coreCoroutineScope")))
@protocol AppKotlinx_coroutines_coreCoroutineScope
@required
@property (readonly) id<AppKotlinCoroutineContext> coroutineContext __attribute__((swift_name("coroutineContext")));
@end


/**
 * @note annotations
 *   androidx.compose.runtime.Stable
*/
__attribute__((swift_name("RuntimeState")))
@protocol AppRuntimeState
@required
@property (readonly) id _Nullable value __attribute__((swift_name("value")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioPath")))
@interface AppOkioPath : AppBase <AppKotlinComparable>
@property (class, readonly, getter=companion) AppOkioPathCompanion *companion __attribute__((swift_name("companion")));
- (int32_t)compareToOther:(AppOkioPath *)other __attribute__((swift_name("compareTo(other:)")));
- (AppOkioPath *)divChild:(NSString *)child __attribute__((swift_name("div(child:)")));
- (AppOkioPath *)divChild_:(AppOkioByteString *)child __attribute__((swift_name("div(child_:)")));
- (AppOkioPath *)divChild__:(AppOkioPath *)child __attribute__((swift_name("div(child__:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (AppOkioPath *)normalized __attribute__((swift_name("normalized()")));
- (AppOkioPath *)relativeToOther:(AppOkioPath *)other __attribute__((swift_name("relativeTo(other:)")));
- (AppOkioPath *)resolveChild:(NSString *)child normalize:(BOOL)normalize __attribute__((swift_name("resolve(child:normalize:)")));
- (AppOkioPath *)resolveChild:(AppOkioByteString *)child normalize_:(BOOL)normalize __attribute__((swift_name("resolve(child:normalize_:)")));
- (AppOkioPath *)resolveChild:(AppOkioPath *)child normalize__:(BOOL)normalize __attribute__((swift_name("resolve(child:normalize__:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) BOOL isAbsolute __attribute__((swift_name("isAbsolute")));
@property (readonly) BOOL isRelative __attribute__((swift_name("isRelative")));
@property (readonly) BOOL isRoot __attribute__((swift_name("isRoot")));
@property (readonly) NSString *name __attribute__((swift_name("name")));
@property (readonly) AppOkioByteString *nameBytes __attribute__((swift_name("nameBytes")));
@property (readonly) AppOkioPath * _Nullable parent __attribute__((swift_name("parent")));
@property (readonly) AppOkioPath * _Nullable root __attribute__((swift_name("root")));
@property (readonly) NSArray<NSString *> *segments __attribute__((swift_name("segments")));
@property (readonly) NSArray<AppOkioByteString *> *segmentsBytes __attribute__((swift_name("segmentsBytes")));
@property (readonly) id _Nullable volumeLetter __attribute__((swift_name("volumeLetter")));
@end

__attribute__((swift_name("Kotlinx_coroutines_coreFlow")))
@protocol AppKotlinx_coroutines_coreFlow
@required

/**
 * @note This method converts instances of CancellationException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (void)collectCollector:(id<AppKotlinx_coroutines_coreFlowCollector>)collector completionHandler:(void (^)(NSError * _Nullable))completionHandler __attribute__((swift_name("collect(collector:completionHandler:)")));
@end

__attribute__((swift_name("Kotlinx_coroutines_coreSharedFlow")))
@protocol AppKotlinx_coroutines_coreSharedFlow <AppKotlinx_coroutines_coreFlow>
@required
@property (readonly) NSArray<id> *replayCache __attribute__((swift_name("replayCache")));
@end

__attribute__((swift_name("Kotlinx_coroutines_coreStateFlow")))
@protocol AppKotlinx_coroutines_coreStateFlow <AppKotlinx_coroutines_coreSharedFlow>
@required
@property (readonly) id _Nullable value __attribute__((swift_name("value")));
@end

__attribute__((swift_name("KotlinThrowable")))
@interface AppKotlinThrowable : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (instancetype)initWithMessage:(NSString * _Nullable)message __attribute__((swift_name("init(message:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithCause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(cause:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithMessage:(NSString * _Nullable)message cause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(message:cause:)"))) __attribute__((objc_designated_initializer));

/**
 * @note annotations
 *   kotlin.experimental.ExperimentalNativeApi
*/
- (AppKotlinArray<NSString *> *)getStackTrace __attribute__((swift_name("getStackTrace()")));
- (void)printStackTrace __attribute__((swift_name("printStackTrace()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) AppKotlinThrowable * _Nullable cause __attribute__((swift_name("cause")));
@property (readonly) NSString * _Nullable message __attribute__((swift_name("message")));
- (NSError *)asError __attribute__((swift_name("asError()")));
@end

__attribute__((swift_name("KotlinException")))
@interface AppKotlinException : AppKotlinThrowable
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (instancetype)initWithMessage:(NSString * _Nullable)message __attribute__((swift_name("init(message:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithCause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(cause:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithMessage:(NSString * _Nullable)message cause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(message:cause:)"))) __attribute__((objc_designated_initializer));
@end

__attribute__((swift_name("KotlinRuntimeException")))
@interface AppKotlinRuntimeException : AppKotlinException
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (instancetype)initWithMessage:(NSString * _Nullable)message __attribute__((swift_name("init(message:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithCause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(cause:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithMessage:(NSString * _Nullable)message cause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(message:cause:)"))) __attribute__((objc_designated_initializer));
@end

__attribute__((swift_name("KotlinIllegalStateException")))
@interface AppKotlinIllegalStateException : AppKotlinRuntimeException
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (instancetype)initWithMessage:(NSString * _Nullable)message __attribute__((swift_name("init(message:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithCause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(cause:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithMessage:(NSString * _Nullable)message cause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(message:cause:)"))) __attribute__((objc_designated_initializer));
@end


/**
 * @note annotations
 *   kotlin.SinceKotlin(version="1.4")
*/
__attribute__((swift_name("KotlinCancellationException")))
@interface AppKotlinCancellationException : AppKotlinIllegalStateException
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (instancetype)initWithMessage:(NSString * _Nullable)message __attribute__((swift_name("init(message:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithCause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(cause:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithMessage:(NSString * _Nullable)message cause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(message:cause:)"))) __attribute__((objc_designated_initializer));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("KotlinEnumCompanion")))
@interface AppKotlinEnumCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppKotlinEnumCompanion *shared __attribute__((swift_name("shared")));
@end


/**
 * @note annotations
 *   androidx.compose.runtime.Stable
*/
__attribute__((swift_name("RuntimeMutableState")))
@protocol AppRuntimeMutableState <AppRuntimeState>
@required
- (void)setValue:(id _Nullable)value __attribute__((swift_name("setValue(_:)")));
- (id _Nullable)component1 __attribute__((swift_name("component1()")));
- (void (^)(id _Nullable))component2 __attribute__((swift_name("component2()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("KotlinByteArray")))
@interface AppKotlinByteArray : AppBase
+ (instancetype)arrayWithSize:(int32_t)size __attribute__((swift_name("init(size:)")));
+ (instancetype)arrayWithSize:(int32_t)size init:(AppByte *(^)(AppInt *))init __attribute__((swift_name("init(size:init:)")));
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
- (int8_t)getIndex:(int32_t)index __attribute__((swift_name("get(index:)")));
- (AppKotlinByteIterator *)iterator __attribute__((swift_name("iterator()")));
- (void)setIndex:(int32_t)index value:(int8_t)value __attribute__((swift_name("set(index:value:)")));
@property (readonly) int32_t size __attribute__((swift_name("size")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreSerializationStrategy")))
@protocol AppKotlinx_serialization_coreSerializationStrategy
@required
- (void)serializeEncoder:(id<AppKotlinx_serialization_coreEncoder>)encoder value:(id _Nullable)value __attribute__((swift_name("serialize(encoder:value:)")));
@property (readonly) id<AppKotlinx_serialization_coreSerialDescriptor> descriptor __attribute__((swift_name("descriptor")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreDeserializationStrategy")))
@protocol AppKotlinx_serialization_coreDeserializationStrategy
@required
- (id _Nullable)deserializeDecoder:(id<AppKotlinx_serialization_coreDecoder>)decoder __attribute__((swift_name("deserialize(decoder:)")));
@property (readonly) id<AppKotlinx_serialization_coreSerialDescriptor> descriptor __attribute__((swift_name("descriptor")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreKSerializer")))
@protocol AppKotlinx_serialization_coreKSerializer <AppKotlinx_serialization_coreSerializationStrategy, AppKotlinx_serialization_coreDeserializationStrategy>
@required
@end

__attribute__((swift_name("OkioFileSystem")))
@interface AppOkioFileSystem : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
@property (class, readonly, getter=companion) AppOkioFileSystemCompanion *companion __attribute__((swift_name("companion")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id<AppOkioSink> _Nullable)appendingSinkFile:(AppOkioPath *)file mustExist:(BOOL)mustExist error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("appendingSink(file:mustExist:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)atomicMoveSource:(AppOkioPath *)source target:(AppOkioPath *)target error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("atomicMove(source:target:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (AppOkioPath * _Nullable)canonicalizePath:(AppOkioPath *)path error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("canonicalize(path:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)doCopySource:(AppOkioPath *)source target:(AppOkioPath *)target error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("doCopy(source:target:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)createDirectoriesDir:(AppOkioPath *)dir mustCreate:(BOOL)mustCreate error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("createDirectories(dir:mustCreate:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)createDirectoryDir:(AppOkioPath *)dir mustCreate:(BOOL)mustCreate error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("createDirectory(dir:mustCreate:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)createSymlinkSource:(AppOkioPath *)source target:(AppOkioPath *)target error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("createSymlink(source:target:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)deletePath:(AppOkioPath *)path mustExist:(BOOL)mustExist error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("delete(path:mustExist:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)deleteRecursivelyFileOrDirectory:(AppOkioPath *)fileOrDirectory mustExist:(BOOL)mustExist error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("deleteRecursively(fileOrDirectory:mustExist:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)existsPath:(AppOkioPath *)path error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("exists(path:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (NSArray<AppOkioPath *> * _Nullable)listDir:(AppOkioPath *)dir error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("list(dir:)")));
- (NSArray<AppOkioPath *> * _Nullable)listOrNullDir:(AppOkioPath *)dir __attribute__((swift_name("listOrNull(dir:)")));
- (id<AppKotlinSequence>)listRecursivelyDir:(AppOkioPath *)dir followSymlinks:(BOOL)followSymlinks __attribute__((swift_name("listRecursively(dir:followSymlinks:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (AppOkioFileMetadata * _Nullable)metadataPath:(AppOkioPath *)path error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("metadata(path:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (AppOkioFileMetadata * _Nullable)metadataOrNullPath:(AppOkioPath *)path error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("metadataOrNull(path:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (AppOkioFileHandle * _Nullable)openReadOnlyFile:(AppOkioPath *)file error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("openReadOnly(file:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (AppOkioFileHandle * _Nullable)openReadWriteFile:(AppOkioPath *)file mustCreate:(BOOL)mustCreate mustExist:(BOOL)mustExist error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("openReadWrite(file:mustCreate:mustExist:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id _Nullable)readFile:(AppOkioPath *)file error:(NSError * _Nullable * _Nullable)error readerAction:(id _Nullable (^)(id<AppOkioBufferedSource>))readerAction __attribute__((swift_name("read(file:readerAction:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id<AppOkioSink> _Nullable)sinkFile:(AppOkioPath *)file mustCreate:(BOOL)mustCreate error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("sink(file:mustCreate:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id<AppOkioSource> _Nullable)sourceFile:(AppOkioPath *)file error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("source(file:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id _Nullable)writeFile:(AppOkioPath *)file mustCreate:(BOOL)mustCreate error:(NSError * _Nullable * _Nullable)error writerAction:(id _Nullable (^)(id<AppOkioBufferedSink>))writerAction __attribute__((swift_name("write(file:mustCreate:writerAction:)"))) __attribute__((swift_error(nonnull_error)));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreModule")))
@interface AppKoin_coreModule : AppBase
- (instancetype)initWith_createdAtStart:(BOOL)_createdAtStart __attribute__((swift_name("init(_createdAtStart:)"))) __attribute__((objc_designated_initializer));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (AppKoin_coreKoinDefinition<id> *)factoryQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier definition:(id _Nullable (^)(AppKoin_coreScope *, AppKoin_coreParametersHolder *))definition __attribute__((swift_name("factory(qualifier:definition:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (void)includesModule:(AppKotlinArray<AppKoin_coreModule *> *)module __attribute__((swift_name("includes(module:)")));
- (void)includesModule_:(id)module __attribute__((swift_name("includes(module_:)")));
- (void)indexPrimaryTypeInstanceFactory:(AppKoin_coreInstanceFactory<id> *)instanceFactory __attribute__((swift_name("indexPrimaryType(instanceFactory:)")));
- (void)indexSecondaryTypesInstanceFactory:(AppKoin_coreInstanceFactory<id> *)instanceFactory __attribute__((swift_name("indexSecondaryTypes(instanceFactory:)")));
- (NSArray<AppKoin_coreModule *> *)plusModules:(NSArray<AppKoin_coreModule *> *)modules __attribute__((swift_name("plus(modules:)")));
- (NSArray<AppKoin_coreModule *> *)plusModule:(AppKoin_coreModule *)module __attribute__((swift_name("plus(module:)")));
- (void)prepareForCreationAtStartInstanceFactory:(AppKoin_coreSingleInstanceFactory<id> *)instanceFactory __attribute__((swift_name("prepareForCreationAtStart(instanceFactory:)")));
- (void)scopeScopeSet:(void (^)(AppKoin_coreScopeDSL *))scopeSet __attribute__((swift_name("scope(scopeSet:)")));
- (void)scopeQualifier:(id<AppKoin_coreQualifier>)qualifier scopeSet:(void (^)(AppKoin_coreScopeDSL *))scopeSet __attribute__((swift_name("scope(qualifier:scopeSet:)")));
- (AppKoin_coreKoinDefinition<id> *)singleQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier createdAtStart:(BOOL)createdAtStart definition:(id _Nullable (^)(AppKoin_coreScope *, AppKoin_coreParametersHolder *))definition __attribute__((swift_name("single(qualifier:createdAtStart:definition:)")));
@property (readonly) AppMutableSet<AppKoin_coreSingleInstanceFactory<id> *> *eagerInstances __attribute__((swift_name("eagerInstances")));
@property (readonly) NSString *id __attribute__((swift_name("id")));
@property (readonly) NSMutableArray<AppKoin_coreModule *> *includedModules __attribute__((swift_name("includedModules")));
@property (readonly) BOOL isLoaded __attribute__((swift_name("isLoaded")));
@property (readonly) AppMutableDictionary<NSString *, AppKoin_coreInstanceFactory<id> *> *mappings __attribute__((swift_name("mappings")));
@end

__attribute__((swift_name("Koin_coreLockable")))
@interface AppKoin_coreLockable : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreScope")))
@interface AppKoin_coreScope : AppKoin_coreLockable
- (instancetype)initWithScopeQualifier:(id<AppKoin_coreQualifier>)scopeQualifier id:(NSString *)id isRoot:(BOOL)isRoot _koin:(AppKoin_coreKoin *)_koin __attribute__((swift_name("init(scopeQualifier:id:isRoot:_koin:)"))) __attribute__((objc_designated_initializer));
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
+ (instancetype)new __attribute__((unavailable));
- (void)close __attribute__((swift_name("close()")));
- (void)declareInstance:(id _Nullable)instance qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier secondaryTypes:(NSArray<id<AppKotlinKClass>> *)secondaryTypes allowOverride:(BOOL)allowOverride __attribute__((swift_name("declare(instance:qualifier:secondaryTypes:allowOverride:)")));
- (id)getQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("get(qualifier:parameters:)")));
- (id _Nullable)getClazz:(id<AppKotlinKClass>)clazz qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("get(clazz:qualifier:parameters:)")));
- (NSArray<id> *)getAll __attribute__((swift_name("getAll()")));
- (NSArray<id> *)getAllClazz:(id<AppKotlinKClass>)clazz __attribute__((swift_name("getAll(clazz:)")));
- (AppKoin_coreKoin *)getKoin __attribute__((swift_name("getKoin()")));
- (id _Nullable)getOrNullQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("getOrNull(qualifier:parameters:)")));
- (id _Nullable)getOrNullClazz:(id<AppKotlinKClass>)clazz qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("getOrNull(clazz:qualifier:parameters:)")));
- (id)getPropertyKey:(NSString *)key __attribute__((swift_name("getProperty(key:)")));
- (id)getPropertyKey:(NSString *)key defaultValue:(id)defaultValue __attribute__((swift_name("getProperty(key:defaultValue:)")));
- (id _Nullable)getPropertyOrNullKey:(NSString *)key __attribute__((swift_name("getPropertyOrNull(key:)")));
- (AppKoin_coreScope *)getScopeScopeID:(NSString *)scopeID __attribute__((swift_name("getScope(scopeID:)")));
- (id _Nullable)getSource __attribute__((swift_name("getSource()")));
- (id<AppKotlinLazy>)injectQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier mode:(AppKotlinLazyThreadSafetyMode *)mode parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("inject(qualifier:mode:parameters:)")));
- (id<AppKotlinLazy>)injectOrNullQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier mode:(AppKotlinLazyThreadSafetyMode *)mode parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("injectOrNull(qualifier:mode:parameters:)")));
- (BOOL)isNotClosed __attribute__((swift_name("isNotClosed()")));
- (void)linkToScopes:(AppKotlinArray<AppKoin_coreScope *> *)scopes __attribute__((swift_name("linkTo(scopes:)")));
- (void)registerCallbackCallback:(id<AppKoin_coreScopeCallback>)callback __attribute__((swift_name("registerCallback(callback:)")));
- (NSString *)description __attribute__((swift_name("description()")));
- (void)unlinkScopes:(AppKotlinArray<AppKoin_coreScope *> *)scopes __attribute__((swift_name("unlink(scopes:)")));
@property (readonly) AppStately_concurrencyThreadLocalRef<NSMutableArray<AppKoin_coreParametersHolder *> *> *_parameterStackLocal __attribute__((swift_name("_parameterStackLocal")));
@property id _Nullable _source __attribute__((swift_name("_source")));
@property (readonly) BOOL closed __attribute__((swift_name("closed")));
@property (readonly) NSString *id __attribute__((swift_name("id")));
@property (readonly) BOOL isRoot __attribute__((swift_name("isRoot")));
@property (readonly) AppKoin_coreLogger *logger __attribute__((swift_name("logger")));
@property (readonly) id<AppKoin_coreQualifier> scopeQualifier __attribute__((swift_name("scopeQualifier")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreKoinApplication")))
@interface AppKoin_coreKoinApplication : AppBase
@property (class, readonly, getter=companion) AppKoin_coreKoinApplicationCompanion *companion __attribute__((swift_name("companion")));
- (void)allowOverrideOverride:(BOOL)override __attribute__((swift_name("allowOverride(override:)")));
- (void)close __attribute__((swift_name("close()")));
- (void)createEagerInstances __attribute__((swift_name("createEagerInstances()")));
- (AppKoin_coreKoinApplication *)loggerLogger:(AppKoin_coreLogger *)logger __attribute__((swift_name("logger(logger:)")));
- (AppKoin_coreKoinApplication *)modulesModules:(AppKotlinArray<AppKoin_coreModule *> *)modules __attribute__((swift_name("modules(modules:)")));
- (AppKoin_coreKoinApplication *)modulesModules_:(NSArray<AppKoin_coreModule *> *)modules __attribute__((swift_name("modules(modules_:)")));
- (AppKoin_coreKoinApplication *)modulesModules__:(AppKoin_coreModule *)modules __attribute__((swift_name("modules(modules__:)")));
- (AppKoin_coreKoinApplication *)printLoggerLevel:(AppKoin_coreLevel *)level __attribute__((swift_name("printLogger(level:)")));
- (AppKoin_coreKoinApplication *)propertiesValues:(NSDictionary<NSString *, id> *)values __attribute__((swift_name("properties(values:)")));
@property (readonly) AppKoin_coreKoin *koin __attribute__((swift_name("koin")));
@end

__attribute__((swift_name("KotlinIterator")))
@protocol AppKotlinIterator
@required
- (BOOL)hasNext __attribute__((swift_name("hasNext()")));
- (id _Nullable)next __attribute__((swift_name("next()")));
@end


/**
 * @note annotations
 *   kotlin.SinceKotlin(version="1.3")
*/
__attribute__((swift_name("KotlinCoroutineContext")))
@protocol AppKotlinCoroutineContext
@required
- (id _Nullable)foldInitial:(id _Nullable)initial operation:(id _Nullable (^)(id _Nullable, id<AppKotlinCoroutineContextElement>))operation __attribute__((swift_name("fold(initial:operation:)")));
- (id<AppKotlinCoroutineContextElement> _Nullable)getKey:(id<AppKotlinCoroutineContextKey>)key __attribute__((swift_name("get(key:)")));
- (id<AppKotlinCoroutineContext>)minusKeyKey:(id<AppKotlinCoroutineContextKey>)key __attribute__((swift_name("minusKey(key:)")));
- (id<AppKotlinCoroutineContext>)plusContext:(id<AppKotlinCoroutineContext>)context __attribute__((swift_name("plus(context:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioPath.Companion")))
@interface AppOkioPathCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppOkioPathCompanion *shared __attribute__((swift_name("shared")));
- (AppOkioPath *)toPath:(NSString *)receiver normalize:(BOOL)normalize __attribute__((swift_name("toPath(_:normalize:)")));
@property (readonly) NSString *DIRECTORY_SEPARATOR __attribute__((swift_name("DIRECTORY_SEPARATOR")));
@end

__attribute__((swift_name("OkioByteString")))
@interface AppOkioByteString : AppBase <AppKotlinComparable>
@property (class, readonly, getter=companion) AppOkioByteStringCompanion *companion __attribute__((swift_name("companion")));
- (NSString *)base64 __attribute__((swift_name("base64()")));
- (NSString *)base64Url __attribute__((swift_name("base64Url()")));
- (int32_t)compareToOther:(AppOkioByteString *)other __attribute__((swift_name("compareTo(other:)")));
- (void)doCopyIntoOffset:(int32_t)offset target:(AppKotlinByteArray *)target targetOffset:(int32_t)targetOffset byteCount:(int32_t)byteCount __attribute__((swift_name("doCopyInto(offset:target:targetOffset:byteCount:)")));
- (BOOL)endsWithSuffix:(AppKotlinByteArray *)suffix __attribute__((swift_name("endsWith(suffix:)")));
- (BOOL)endsWithSuffix_:(AppOkioByteString *)suffix __attribute__((swift_name("endsWith(suffix_:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (int8_t)getIndex:(int32_t)index __attribute__((swift_name("get(index:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)hex __attribute__((swift_name("hex()")));
- (AppOkioByteString *)hmacSha1Key:(AppOkioByteString *)key __attribute__((swift_name("hmacSha1(key:)")));
- (AppOkioByteString *)hmacSha256Key:(AppOkioByteString *)key __attribute__((swift_name("hmacSha256(key:)")));
- (AppOkioByteString *)hmacSha512Key:(AppOkioByteString *)key __attribute__((swift_name("hmacSha512(key:)")));
- (int32_t)indexOfOther:(AppKotlinByteArray *)other fromIndex:(int32_t)fromIndex __attribute__((swift_name("indexOf(other:fromIndex:)")));
- (int32_t)indexOfOther:(AppOkioByteString *)other fromIndex_:(int32_t)fromIndex __attribute__((swift_name("indexOf(other:fromIndex_:)")));
- (int32_t)lastIndexOfOther:(AppKotlinByteArray *)other fromIndex:(int32_t)fromIndex __attribute__((swift_name("lastIndexOf(other:fromIndex:)")));
- (int32_t)lastIndexOfOther:(AppOkioByteString *)other fromIndex_:(int32_t)fromIndex __attribute__((swift_name("lastIndexOf(other:fromIndex_:)")));
- (AppOkioByteString *)md5 __attribute__((swift_name("md5()")));
- (BOOL)rangeEqualsOffset:(int32_t)offset other:(AppKotlinByteArray *)other otherOffset:(int32_t)otherOffset byteCount:(int32_t)byteCount __attribute__((swift_name("rangeEquals(offset:other:otherOffset:byteCount:)")));
- (BOOL)rangeEqualsOffset:(int32_t)offset other:(AppOkioByteString *)other otherOffset:(int32_t)otherOffset byteCount_:(int32_t)byteCount __attribute__((swift_name("rangeEquals(offset:other:otherOffset:byteCount_:)")));
- (AppOkioByteString *)sha1 __attribute__((swift_name("sha1()")));
- (AppOkioByteString *)sha256 __attribute__((swift_name("sha256()")));
- (AppOkioByteString *)sha512 __attribute__((swift_name("sha512()")));
- (BOOL)startsWithPrefix:(AppKotlinByteArray *)prefix __attribute__((swift_name("startsWith(prefix:)")));
- (BOOL)startsWithPrefix_:(AppOkioByteString *)prefix __attribute__((swift_name("startsWith(prefix_:)")));
- (AppOkioByteString *)substringBeginIndex:(int32_t)beginIndex endIndex:(int32_t)endIndex __attribute__((swift_name("substring(beginIndex:endIndex:)")));
- (AppOkioByteString *)toAsciiLowercase __attribute__((swift_name("toAsciiLowercase()")));
- (AppOkioByteString *)toAsciiUppercase __attribute__((swift_name("toAsciiUppercase()")));
- (AppKotlinByteArray *)toByteArray __attribute__((swift_name("toByteArray()")));
- (NSString *)description __attribute__((swift_name("description()")));
- (NSString *)utf8 __attribute__((swift_name("utf8()")));
@property (readonly) int32_t size __attribute__((swift_name("size")));
@end

__attribute__((swift_name("Kotlinx_coroutines_coreFlowCollector")))
@protocol AppKotlinx_coroutines_coreFlowCollector
@required

/**
 * @note This method converts instances of CancellationException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (void)emitValue:(id _Nullable)value completionHandler:(void (^)(NSError * _Nullable))completionHandler __attribute__((swift_name("emit(value:completionHandler:)")));
@end

__attribute__((swift_name("KotlinByteIterator")))
@interface AppKotlinByteIterator : AppBase <AppKotlinIterator>
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (AppByte *)next __attribute__((swift_name("next()")));
- (int8_t)nextByte __attribute__((swift_name("nextByte()")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreEncoder")))
@protocol AppKotlinx_serialization_coreEncoder
@required
- (id<AppKotlinx_serialization_coreCompositeEncoder>)beginCollectionDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor collectionSize:(int32_t)collectionSize __attribute__((swift_name("beginCollection(descriptor:collectionSize:)")));
- (id<AppKotlinx_serialization_coreCompositeEncoder>)beginStructureDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("beginStructure(descriptor:)")));
- (void)encodeBooleanValue:(BOOL)value __attribute__((swift_name("encodeBoolean(value:)")));
- (void)encodeByteValue:(int8_t)value __attribute__((swift_name("encodeByte(value:)")));
- (void)encodeCharValue:(unichar)value __attribute__((swift_name("encodeChar(value:)")));
- (void)encodeDoubleValue:(double)value __attribute__((swift_name("encodeDouble(value:)")));
- (void)encodeEnumEnumDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)enumDescriptor index:(int32_t)index __attribute__((swift_name("encodeEnum(enumDescriptor:index:)")));
- (void)encodeFloatValue:(float)value __attribute__((swift_name("encodeFloat(value:)")));
- (id<AppKotlinx_serialization_coreEncoder>)encodeInlineDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("encodeInline(descriptor:)")));
- (void)encodeIntValue:(int32_t)value __attribute__((swift_name("encodeInt(value:)")));
- (void)encodeLongValue:(int64_t)value __attribute__((swift_name("encodeLong(value:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (void)encodeNotNullMark __attribute__((swift_name("encodeNotNullMark()")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (void)encodeNull __attribute__((swift_name("encodeNull()")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (void)encodeNullableSerializableValueSerializer:(id<AppKotlinx_serialization_coreSerializationStrategy>)serializer value:(id _Nullable)value __attribute__((swift_name("encodeNullableSerializableValue(serializer:value:)")));
- (void)encodeSerializableValueSerializer:(id<AppKotlinx_serialization_coreSerializationStrategy>)serializer value:(id _Nullable)value __attribute__((swift_name("encodeSerializableValue(serializer:value:)")));
- (void)encodeShortValue:(int16_t)value __attribute__((swift_name("encodeShort(value:)")));
- (void)encodeStringValue:(NSString *)value __attribute__((swift_name("encodeString(value:)")));
@property (readonly) AppKotlinx_serialization_coreSerializersModule *serializersModule __attribute__((swift_name("serializersModule")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreSerialDescriptor")))
@protocol AppKotlinx_serialization_coreSerialDescriptor
@required

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (NSArray<id<AppKotlinAnnotation>> *)getElementAnnotationsIndex:(int32_t)index __attribute__((swift_name("getElementAnnotations(index:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (id<AppKotlinx_serialization_coreSerialDescriptor>)getElementDescriptorIndex:(int32_t)index __attribute__((swift_name("getElementDescriptor(index:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (int32_t)getElementIndexName:(NSString *)name __attribute__((swift_name("getElementIndex(name:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (NSString *)getElementNameIndex:(int32_t)index __attribute__((swift_name("getElementName(index:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (BOOL)isElementOptionalIndex:(int32_t)index __attribute__((swift_name("isElementOptional(index:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
@property (readonly) NSArray<id<AppKotlinAnnotation>> *annotations __attribute__((swift_name("annotations")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
@property (readonly) int32_t elementsCount __attribute__((swift_name("elementsCount")));
@property (readonly) BOOL isInline __attribute__((swift_name("isInline")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
@property (readonly) BOOL isNullable __attribute__((swift_name("isNullable")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
@property (readonly) AppKotlinx_serialization_coreSerialKind *kind __attribute__((swift_name("kind")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
@property (readonly) NSString *serialName __attribute__((swift_name("serialName")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreDecoder")))
@protocol AppKotlinx_serialization_coreDecoder
@required
- (id<AppKotlinx_serialization_coreCompositeDecoder>)beginStructureDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("beginStructure(descriptor:)")));
- (BOOL)decodeBoolean __attribute__((swift_name("decodeBoolean()")));
- (int8_t)decodeByte __attribute__((swift_name("decodeByte()")));
- (unichar)decodeChar __attribute__((swift_name("decodeChar()")));
- (double)decodeDouble __attribute__((swift_name("decodeDouble()")));
- (int32_t)decodeEnumEnumDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)enumDescriptor __attribute__((swift_name("decodeEnum(enumDescriptor:)")));
- (float)decodeFloat __attribute__((swift_name("decodeFloat()")));
- (id<AppKotlinx_serialization_coreDecoder>)decodeInlineDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("decodeInline(descriptor:)")));
- (int32_t)decodeInt __attribute__((swift_name("decodeInt()")));
- (int64_t)decodeLong __attribute__((swift_name("decodeLong()")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (BOOL)decodeNotNullMark __attribute__((swift_name("decodeNotNullMark()")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (AppKotlinNothing * _Nullable)decodeNull __attribute__((swift_name("decodeNull()")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (id _Nullable)decodeNullableSerializableValueDeserializer:(id<AppKotlinx_serialization_coreDeserializationStrategy>)deserializer __attribute__((swift_name("decodeNullableSerializableValue(deserializer:)")));
- (id _Nullable)decodeSerializableValueDeserializer:(id<AppKotlinx_serialization_coreDeserializationStrategy>)deserializer __attribute__((swift_name("decodeSerializableValue(deserializer:)")));
- (int16_t)decodeShort __attribute__((swift_name("decodeShort()")));
- (NSString *)decodeString __attribute__((swift_name("decodeString()")));
@property (readonly) AppKotlinx_serialization_coreSerializersModule *serializersModule __attribute__((swift_name("serializersModule")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioFileSystem.Companion")))
@interface AppOkioFileSystemCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppOkioFileSystemCompanion *shared __attribute__((swift_name("shared")));
@property (readonly) AppOkioFileSystem *SYSTEM __attribute__((swift_name("SYSTEM")));
@property (readonly) AppOkioPath *SYSTEM_TEMPORARY_DIRECTORY __attribute__((swift_name("SYSTEM_TEMPORARY_DIRECTORY")));
@end

__attribute__((swift_name("OkioIOException")))
@interface AppOkioIOException : AppKotlinException
- (instancetype)initWithMessage:(NSString * _Nullable)message __attribute__((swift_name("init(message:)"))) __attribute__((objc_designated_initializer));
- (instancetype)initWithMessage:(NSString * _Nullable)message cause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(message:cause:)"))) __attribute__((objc_designated_initializer));
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
+ (instancetype)new __attribute__((unavailable));
- (instancetype)initWithCause:(AppKotlinThrowable * _Nullable)cause __attribute__((swift_name("init(cause:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
@end

__attribute__((swift_name("OkioCloseable")))
@protocol AppOkioCloseable
@required

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)closeAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("close_()")));
@end

__attribute__((swift_name("OkioSink")))
@protocol AppOkioSink <AppOkioCloseable>
@required

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)flushAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("flush()")));
- (AppOkioTimeout *)timeout __attribute__((swift_name("timeout()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)writeSource:(AppOkioBuffer *)source byteCount:(int64_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("write(source:byteCount:)")));
@end

__attribute__((swift_name("KotlinSequence")))
@protocol AppKotlinSequence
@required
- (id<AppKotlinIterator>)iterator __attribute__((swift_name("iterator()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioFileMetadata")))
@interface AppOkioFileMetadata : AppBase
- (instancetype)initWithIsRegularFile:(BOOL)isRegularFile isDirectory:(BOOL)isDirectory symlinkTarget:(AppOkioPath * _Nullable)symlinkTarget size:(AppLong * _Nullable)size createdAtMillis:(AppLong * _Nullable)createdAtMillis lastModifiedAtMillis:(AppLong * _Nullable)lastModifiedAtMillis lastAccessedAtMillis:(AppLong * _Nullable)lastAccessedAtMillis extras:(NSDictionary<id<AppKotlinKClass>, id> *)extras __attribute__((swift_name("init(isRegularFile:isDirectory:symlinkTarget:size:createdAtMillis:lastModifiedAtMillis:lastAccessedAtMillis:extras:)"))) __attribute__((objc_designated_initializer));
- (AppOkioFileMetadata *)doCopyIsRegularFile:(BOOL)isRegularFile isDirectory:(BOOL)isDirectory symlinkTarget:(AppOkioPath * _Nullable)symlinkTarget size:(AppLong * _Nullable)size createdAtMillis:(AppLong * _Nullable)createdAtMillis lastModifiedAtMillis:(AppLong * _Nullable)lastModifiedAtMillis lastAccessedAtMillis:(AppLong * _Nullable)lastAccessedAtMillis extras:(NSDictionary<id<AppKotlinKClass>, id> *)extras __attribute__((swift_name("doCopy(isRegularFile:isDirectory:symlinkTarget:size:createdAtMillis:lastModifiedAtMillis:lastAccessedAtMillis:extras:)")));
- (id _Nullable)extraType:(id<AppKotlinKClass>)type __attribute__((swift_name("extra(type:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) AppLong * _Nullable createdAtMillis __attribute__((swift_name("createdAtMillis")));
@property (readonly) NSDictionary<id<AppKotlinKClass>, id> *extras __attribute__((swift_name("extras")));
@property (readonly) BOOL isDirectory __attribute__((swift_name("isDirectory")));
@property (readonly) BOOL isRegularFile __attribute__((swift_name("isRegularFile")));
@property (readonly) AppLong * _Nullable lastAccessedAtMillis __attribute__((swift_name("lastAccessedAtMillis")));
@property (readonly) AppLong * _Nullable lastModifiedAtMillis __attribute__((swift_name("lastModifiedAtMillis")));
@property (readonly) AppLong * _Nullable size __attribute__((swift_name("size")));
@property (readonly) AppOkioPath * _Nullable symlinkTarget __attribute__((swift_name("symlinkTarget")));
@end

__attribute__((swift_name("OkioFileHandle")))
@interface AppOkioFileHandle : AppBase <AppOkioCloseable>
- (instancetype)initWithReadWrite:(BOOL)readWrite __attribute__((swift_name("init(readWrite:)"))) __attribute__((objc_designated_initializer));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id<AppOkioSink> _Nullable)appendingSinkAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("appendingSink()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)closeAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("close_()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)flushAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("flush()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (int64_t)positionSink:(id<AppOkioSink>)sink error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("position(sink:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (int64_t)positionSource:(id<AppOkioSource>)source error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("position(source:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (BOOL)protectedCloseAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("protectedClose()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (BOOL)protectedFlushAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("protectedFlush()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (int32_t)protectedReadFileOffset:(int64_t)fileOffset array:(AppKotlinByteArray *)array arrayOffset:(int32_t)arrayOffset byteCount:(int32_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("protectedRead(fileOffset:array:arrayOffset:byteCount:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (BOOL)protectedResizeSize:(int64_t)size error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("protectedResize(size:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (int64_t)protectedSizeAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("protectedSize()"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
 * @note This method has protected visibility in Kotlin source and is intended only for use by subclasses.
*/
- (BOOL)protectedWriteFileOffset:(int64_t)fileOffset array:(AppKotlinByteArray *)array arrayOffset:(int32_t)arrayOffset byteCount:(int32_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("protectedWrite(fileOffset:array:arrayOffset:byteCount:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (int64_t)readFileOffset:(int64_t)fileOffset sink:(AppOkioBuffer *)sink byteCount:(int64_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("read(fileOffset:sink:byteCount:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (int32_t)readFileOffset:(int64_t)fileOffset array:(AppKotlinByteArray *)array arrayOffset:(int32_t)arrayOffset byteCount:(int32_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("read(fileOffset:array:arrayOffset:byteCount:)"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)repositionSink:(id<AppOkioSink>)sink position:(int64_t)position error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("reposition(sink:position:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)repositionSource:(id<AppOkioSource>)source position:(int64_t)position error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("reposition(source:position:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)resizeSize:(int64_t)size error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("resize(size:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id<AppOkioSink> _Nullable)sinkFileOffset:(int64_t)fileOffset error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("sink(fileOffset:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (int64_t)sizeAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("size()"))) __attribute__((swift_error(nonnull_error)));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (id<AppOkioSource> _Nullable)sourceFileOffset:(int64_t)fileOffset error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("source(fileOffset:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)writeFileOffset:(int64_t)fileOffset source:(AppOkioBuffer *)source byteCount:(int64_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("write(fileOffset:source:byteCount:)")));
- (void)writeFileOffset:(int64_t)fileOffset array:(AppKotlinByteArray *)array arrayOffset:(int32_t)arrayOffset byteCount:(int32_t)byteCount __attribute__((swift_name("write(fileOffset:array:arrayOffset:byteCount:)")));
@property (readonly) AppOkioLock *lock __attribute__((swift_name("lock")));
@property (readonly) BOOL readWrite __attribute__((swift_name("readWrite")));
@end

__attribute__((swift_name("OkioSource")))
@protocol AppOkioSource <AppOkioCloseable>
@required

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (int64_t)readSink:(AppOkioBuffer *)sink byteCount:(int64_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("read(sink:byteCount:)"))) __attribute__((swift_error(nonnull_error)));
- (AppOkioTimeout *)timeout __attribute__((swift_name("timeout()")));
@end

__attribute__((swift_name("OkioBufferedSource")))
@protocol AppOkioBufferedSource <AppOkioSource>
@required
- (BOOL)exhausted __attribute__((swift_name("exhausted()")));
- (int64_t)indexOfB:(int8_t)b __attribute__((swift_name("indexOf(b:)")));
- (int64_t)indexOfBytes:(AppOkioByteString *)bytes __attribute__((swift_name("indexOf(bytes:)")));
- (int64_t)indexOfB:(int8_t)b fromIndex:(int64_t)fromIndex __attribute__((swift_name("indexOf(b:fromIndex:)")));
- (int64_t)indexOfBytes:(AppOkioByteString *)bytes fromIndex:(int64_t)fromIndex __attribute__((swift_name("indexOf(bytes:fromIndex:)")));
- (int64_t)indexOfB:(int8_t)b fromIndex:(int64_t)fromIndex toIndex:(int64_t)toIndex __attribute__((swift_name("indexOf(b:fromIndex:toIndex:)")));
- (int64_t)indexOfElementTargetBytes:(AppOkioByteString *)targetBytes __attribute__((swift_name("indexOfElement(targetBytes:)")));
- (int64_t)indexOfElementTargetBytes:(AppOkioByteString *)targetBytes fromIndex:(int64_t)fromIndex __attribute__((swift_name("indexOfElement(targetBytes:fromIndex:)")));
- (id<AppOkioBufferedSource>)peek __attribute__((swift_name("peek()")));
- (BOOL)rangeEqualsOffset:(int64_t)offset bytes:(AppOkioByteString *)bytes __attribute__((swift_name("rangeEquals(offset:bytes:)")));
- (BOOL)rangeEqualsOffset:(int64_t)offset bytes:(AppOkioByteString *)bytes bytesOffset:(int32_t)bytesOffset byteCount:(int32_t)byteCount __attribute__((swift_name("rangeEquals(offset:bytes:bytesOffset:byteCount:)")));
- (int32_t)readSink:(AppKotlinByteArray *)sink __attribute__((swift_name("read(sink:)")));
- (int32_t)readSink:(AppKotlinByteArray *)sink offset:(int32_t)offset byteCount:(int32_t)byteCount __attribute__((swift_name("read(sink:offset:byteCount:)")));
- (int64_t)readAllSink:(id<AppOkioSink>)sink __attribute__((swift_name("readAll(sink:)")));
- (int8_t)readByte __attribute__((swift_name("readByte()")));
- (AppKotlinByteArray *)readByteArray __attribute__((swift_name("readByteArray()")));
- (AppKotlinByteArray *)readByteArrayByteCount:(int64_t)byteCount __attribute__((swift_name("readByteArray(byteCount:)")));
- (AppOkioByteString *)readByteString __attribute__((swift_name("readByteString()")));
- (AppOkioByteString *)readByteStringByteCount:(int64_t)byteCount __attribute__((swift_name("readByteString(byteCount:)")));
- (int64_t)readDecimalLong __attribute__((swift_name("readDecimalLong()")));
- (void)readFullySink:(AppKotlinByteArray *)sink __attribute__((swift_name("readFully(sink:)")));
- (void)readFullySink:(AppOkioBuffer *)sink byteCount:(int64_t)byteCount __attribute__((swift_name("readFully(sink:byteCount:)")));
- (int64_t)readHexadecimalUnsignedLong __attribute__((swift_name("readHexadecimalUnsignedLong()")));
- (int32_t)readInt __attribute__((swift_name("readInt()")));
- (int32_t)readIntLe __attribute__((swift_name("readIntLe()")));
- (int64_t)readLong __attribute__((swift_name("readLong()")));
- (int64_t)readLongLe __attribute__((swift_name("readLongLe()")));
- (int16_t)readShort __attribute__((swift_name("readShort()")));
- (int16_t)readShortLe __attribute__((swift_name("readShortLe()")));
- (NSString *)readUtf8 __attribute__((swift_name("readUtf8()")));
- (NSString *)readUtf8ByteCount:(int64_t)byteCount __attribute__((swift_name("readUtf8(byteCount:)")));
- (int32_t)readUtf8CodePoint __attribute__((swift_name("readUtf8CodePoint()")));
- (NSString * _Nullable)readUtf8Line __attribute__((swift_name("readUtf8Line()")));
- (NSString *)readUtf8LineStrict __attribute__((swift_name("readUtf8LineStrict()")));
- (NSString *)readUtf8LineStrictLimit:(int64_t)limit __attribute__((swift_name("readUtf8LineStrict(limit:)")));
- (BOOL)requestByteCount:(int64_t)byteCount __attribute__((swift_name("request(byteCount:)")));
- (void)requireByteCount:(int64_t)byteCount __attribute__((swift_name("require(byteCount:)")));
- (int32_t)selectOptions:(NSArray<AppOkioByteString *> *)options __attribute__((swift_name("select(options:)")));
- (void)skipByteCount:(int64_t)byteCount __attribute__((swift_name("skip(byteCount:)")));
@property (readonly) AppOkioBuffer *buffer __attribute__((swift_name("buffer")));
@end

__attribute__((swift_name("OkioBufferedSink")))
@protocol AppOkioBufferedSink <AppOkioSink>
@required
- (id<AppOkioBufferedSink>)emit __attribute__((swift_name("emit()")));
- (id<AppOkioBufferedSink>)emitCompleteSegments __attribute__((swift_name("emitCompleteSegments()")));
- (id<AppOkioBufferedSink>)writeSource:(AppKotlinByteArray *)source __attribute__((swift_name("write(source:)")));
- (id<AppOkioBufferedSink>)writeByteString:(AppOkioByteString *)byteString __attribute__((swift_name("write(byteString:)")));
- (id<AppOkioBufferedSink>)writeSource:(id<AppOkioSource>)source byteCount:(int64_t)byteCount __attribute__((swift_name("write(source:byteCount_:)")));
- (id<AppOkioBufferedSink>)writeSource:(AppKotlinByteArray *)source offset:(int32_t)offset byteCount:(int32_t)byteCount __attribute__((swift_name("write(source:offset:byteCount:)")));
- (id<AppOkioBufferedSink>)writeByteString:(AppOkioByteString *)byteString offset:(int32_t)offset byteCount:(int32_t)byteCount __attribute__((swift_name("write(byteString:offset:byteCount:)")));
- (int64_t)writeAllSource:(id<AppOkioSource>)source __attribute__((swift_name("writeAll(source:)")));
- (id<AppOkioBufferedSink>)writeByteB:(int32_t)b __attribute__((swift_name("writeByte(b:)")));
- (id<AppOkioBufferedSink>)writeDecimalLongV:(int64_t)v __attribute__((swift_name("writeDecimalLong(v:)")));
- (id<AppOkioBufferedSink>)writeHexadecimalUnsignedLongV:(int64_t)v __attribute__((swift_name("writeHexadecimalUnsignedLong(v:)")));
- (id<AppOkioBufferedSink>)writeIntI:(int32_t)i __attribute__((swift_name("writeInt(i:)")));
- (id<AppOkioBufferedSink>)writeIntLeI:(int32_t)i __attribute__((swift_name("writeIntLe(i:)")));
- (id<AppOkioBufferedSink>)writeLongV:(int64_t)v __attribute__((swift_name("writeLong(v:)")));
- (id<AppOkioBufferedSink>)writeLongLeV:(int64_t)v __attribute__((swift_name("writeLongLe(v:)")));
- (id<AppOkioBufferedSink>)writeShortS:(int32_t)s __attribute__((swift_name("writeShort(s:)")));
- (id<AppOkioBufferedSink>)writeShortLeS:(int32_t)s __attribute__((swift_name("writeShortLe(s:)")));
- (id<AppOkioBufferedSink>)writeUtf8String:(NSString *)string __attribute__((swift_name("writeUtf8(string:)")));
- (id<AppOkioBufferedSink>)writeUtf8String:(NSString *)string beginIndex:(int32_t)beginIndex endIndex:(int32_t)endIndex __attribute__((swift_name("writeUtf8(string:beginIndex:endIndex:)")));
- (id<AppOkioBufferedSink>)writeUtf8CodePointCodePoint:(int32_t)codePoint __attribute__((swift_name("writeUtf8CodePoint(codePoint:)")));
@property (readonly) AppOkioBuffer *buffer __attribute__((swift_name("buffer")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreKoinDefinition")))
@interface AppKoin_coreKoinDefinition<R> : AppBase
- (instancetype)initWithModule:(AppKoin_coreModule *)module factory:(AppKoin_coreInstanceFactory<R> *)factory __attribute__((swift_name("init(module:factory:)"))) __attribute__((objc_designated_initializer));
- (AppKoin_coreKoinDefinition<R> *)doCopyModule:(AppKoin_coreModule *)module factory:(AppKoin_coreInstanceFactory<R> *)factory __attribute__((swift_name("doCopy(module:factory:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) AppKoin_coreInstanceFactory<R> *factory __attribute__((swift_name("factory")));
@property (readonly) AppKoin_coreModule *module __attribute__((swift_name("module")));
@end

__attribute__((swift_name("Koin_coreQualifier")))
@protocol AppKoin_coreQualifier
@required
@property (readonly) NSString *value __attribute__((swift_name("value")));
@end

__attribute__((swift_name("Koin_coreParametersHolder")))
@interface AppKoin_coreParametersHolder : AppBase
- (instancetype)initWith_values:(NSMutableArray<id> *)_values useIndexedValues:(AppBoolean * _Nullable)useIndexedValues __attribute__((swift_name("init(_values:useIndexedValues:)"))) __attribute__((objc_designated_initializer));
- (AppKoin_coreParametersHolder *)addValue:(id)value __attribute__((swift_name("add(value:)")));
- (id _Nullable)component1 __attribute__((swift_name("component1()")));
- (id _Nullable)component2_ __attribute__((swift_name("component2_()")));
- (id _Nullable)component3 __attribute__((swift_name("component3()")));
- (id _Nullable)component4 __attribute__((swift_name("component4()")));
- (id _Nullable)component5 __attribute__((swift_name("component5()")));
- (id _Nullable)elementAtI:(int32_t)i clazz:(id<AppKotlinKClass>)clazz __attribute__((swift_name("elementAt(i:clazz:)")));
- (id)get __attribute__((swift_name("get()")));
- (id _Nullable)getI:(int32_t)i __attribute__((swift_name("get(i:)")));
- (id _Nullable)getOrNull __attribute__((swift_name("getOrNull()")));
- (id _Nullable)getOrNullClazz:(id<AppKotlinKClass>)clazz __attribute__((swift_name("getOrNull(clazz:)")));
- (AppKoin_coreParametersHolder *)insertIndex:(int32_t)index value:(id)value __attribute__((swift_name("insert(index:value:)")));
- (BOOL)isEmpty __attribute__((swift_name("isEmpty()")));
- (BOOL)isNotEmpty __attribute__((swift_name("isNotEmpty()")));
- (void)setI:(int32_t)i t:(id _Nullable)t __attribute__((swift_name("set(i:t:)")));
- (int32_t)size __attribute__((swift_name("size()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property int32_t index __attribute__((swift_name("index")));
@property (readonly) AppBoolean * _Nullable useIndexedValues __attribute__((swift_name("useIndexedValues")));
@property (readonly) NSArray<id> *values __attribute__((swift_name("values")));
@end

__attribute__((swift_name("Koin_coreInstanceFactory")))
@interface AppKoin_coreInstanceFactory<T> : AppKoin_coreLockable
- (instancetype)initWithBeanDefinition:(AppKoin_coreBeanDefinition<T> *)beanDefinition __attribute__((swift_name("init(beanDefinition:)"))) __attribute__((objc_designated_initializer));
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
+ (instancetype)new __attribute__((unavailable));
@property (class, readonly, getter=companion) AppKoin_coreInstanceFactoryCompanion *companion __attribute__((swift_name("companion")));
- (T _Nullable)createContext:(AppKoin_coreInstanceContext *)context __attribute__((swift_name("create(context:)")));
- (void)dropScope:(AppKoin_coreScope * _Nullable)scope __attribute__((swift_name("drop(scope:)")));
- (void)dropAll __attribute__((swift_name("dropAll()")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (T _Nullable)getContext:(AppKoin_coreInstanceContext *)context __attribute__((swift_name("get(context:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (BOOL)isCreatedContext:(AppKoin_coreInstanceContext * _Nullable)context __attribute__((swift_name("isCreated(context:)")));
@property (readonly) AppKoin_coreBeanDefinition<T> *beanDefinition __attribute__((swift_name("beanDefinition")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreSingleInstanceFactory")))
@interface AppKoin_coreSingleInstanceFactory<T> : AppKoin_coreInstanceFactory<T>
- (instancetype)initWithBeanDefinition:(AppKoin_coreBeanDefinition<T> *)beanDefinition __attribute__((swift_name("init(beanDefinition:)"))) __attribute__((objc_designated_initializer));
- (T _Nullable)createContext:(AppKoin_coreInstanceContext *)context __attribute__((swift_name("create(context:)")));
- (void)dropScope:(AppKoin_coreScope * _Nullable)scope __attribute__((swift_name("drop(scope:)")));
- (void)dropAll __attribute__((swift_name("dropAll()")));
- (T _Nullable)getContext:(AppKoin_coreInstanceContext *)context __attribute__((swift_name("get(context:)")));
- (BOOL)isCreatedContext:(AppKoin_coreInstanceContext * _Nullable)context __attribute__((swift_name("isCreated(context:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreScopeDSL")))
@interface AppKoin_coreScopeDSL : AppBase
- (instancetype)initWithScopeQualifier:(id<AppKoin_coreQualifier>)scopeQualifier module:(AppKoin_coreModule *)module __attribute__((swift_name("init(scopeQualifier:module:)"))) __attribute__((objc_designated_initializer));
- (AppKoin_coreKoinDefinition<id> *)factoryQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier definition:(id _Nullable (^)(AppKoin_coreScope *, AppKoin_coreParametersHolder *))definition __attribute__((swift_name("factory(qualifier:definition:)")));
- (AppKoin_coreKoinDefinition<id> *)scopedQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier definition:(id _Nullable (^)(AppKoin_coreScope *, AppKoin_coreParametersHolder *))definition __attribute__((swift_name("scoped(qualifier:definition:)")));
@property (readonly) AppKoin_coreModule *module __attribute__((swift_name("module")));
@property (readonly) id<AppKoin_coreQualifier> scopeQualifier __attribute__((swift_name("scopeQualifier")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreKoin")))
@interface AppKoin_coreKoin : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (void)close __attribute__((swift_name("close()")));
- (void)createEagerInstances __attribute__((swift_name("createEagerInstances()")));
- (AppKoin_coreScope *)createScopeT:(id<AppKoin_coreKoinScopeComponent>)t __attribute__((swift_name("createScope(t:)")));
- (AppKoin_coreScope *)createScopeScopeId:(NSString *)scopeId __attribute__((swift_name("createScope(scopeId:)")));
- (AppKoin_coreScope *)createScopeScopeId:(NSString *)scopeId source:(id _Nullable)source __attribute__((swift_name("createScope(scopeId:source:)")));
- (AppKoin_coreScope *)createScopeScopeId:(NSString *)scopeId qualifier:(id<AppKoin_coreQualifier>)qualifier source:(id _Nullable)source __attribute__((swift_name("createScope(scopeId:qualifier:source:)")));
- (void)declareInstance:(id _Nullable)instance qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier secondaryTypes:(NSArray<id<AppKotlinKClass>> *)secondaryTypes allowOverride:(BOOL)allowOverride __attribute__((swift_name("declare(instance:qualifier:secondaryTypes:allowOverride:)")));
- (void)deletePropertyKey:(NSString *)key __attribute__((swift_name("deleteProperty(key:)")));
- (void)deleteScopeScopeId:(NSString *)scopeId __attribute__((swift_name("deleteScope(scopeId:)")));
- (id)getQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("get(qualifier:parameters:)")));
- (id _Nullable)getClazz:(id<AppKotlinKClass>)clazz qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("get(clazz:qualifier:parameters:)")));
- (NSArray<id> *)getAll __attribute__((swift_name("getAll()")));
- (AppKoin_coreScope *)getOrCreateScopeScopeId:(NSString *)scopeId __attribute__((swift_name("getOrCreateScope(scopeId:)")));
- (AppKoin_coreScope *)getOrCreateScopeScopeId:(NSString *)scopeId qualifier:(id<AppKoin_coreQualifier>)qualifier source:(id _Nullable)source __attribute__((swift_name("getOrCreateScope(scopeId:qualifier:source:)")));
- (id _Nullable)getOrNullQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("getOrNull(qualifier:parameters:)")));
- (id _Nullable)getOrNullClazz:(id<AppKotlinKClass>)clazz qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("getOrNull(clazz:qualifier:parameters:)")));
- (id _Nullable)getPropertyKey:(NSString *)key __attribute__((swift_name("getProperty(key:)")));
- (id)getPropertyKey:(NSString *)key defaultValue:(id)defaultValue __attribute__((swift_name("getProperty(key:defaultValue:)")));
- (AppKoin_coreScope *)getScopeScopeId:(NSString *)scopeId __attribute__((swift_name("getScope(scopeId:)")));
- (AppKoin_coreScope * _Nullable)getScopeOrNullScopeId:(NSString *)scopeId __attribute__((swift_name("getScopeOrNull(scopeId:)")));
- (id<AppKotlinLazy>)injectQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier mode:(AppKotlinLazyThreadSafetyMode *)mode parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("inject(qualifier:mode:parameters:)")));
- (id<AppKotlinLazy>)injectOrNullQualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier mode:(AppKotlinLazyThreadSafetyMode *)mode parameters:(AppKoin_coreParametersHolder *(^ _Nullable)(void))parameters __attribute__((swift_name("injectOrNull(qualifier:mode:parameters:)")));
- (void)loadModulesModules:(NSArray<AppKoin_coreModule *> *)modules allowOverride:(BOOL)allowOverride createEagerInstances:(BOOL)createEagerInstances __attribute__((swift_name("loadModules(modules:allowOverride:createEagerInstances:)")));
- (void)setPropertyKey:(NSString *)key value:(id)value __attribute__((swift_name("setProperty(key:value:)")));
- (void)setupLoggerLogger:(AppKoin_coreLogger *)logger __attribute__((swift_name("setupLogger(logger:)")));
- (void)unloadModulesModules:(NSArray<AppKoin_coreModule *> *)modules __attribute__((swift_name("unloadModules(modules:)")));
@property (readonly) AppKoin_coreExtensionManager *extensionManager __attribute__((swift_name("extensionManager")));
@property (readonly) AppKoin_coreInstanceRegistry *instanceRegistry __attribute__((swift_name("instanceRegistry")));
@property (readonly) AppKoin_coreLogger *logger __attribute__((swift_name("logger")));
@property (readonly) AppKoin_corePropertyRegistry *propertyRegistry __attribute__((swift_name("propertyRegistry")));
@property (readonly) AppKoin_coreScopeRegistry *scopeRegistry __attribute__((swift_name("scopeRegistry")));
@end

__attribute__((swift_name("KotlinKDeclarationContainer")))
@protocol AppKotlinKDeclarationContainer
@required
@end

__attribute__((swift_name("KotlinKAnnotatedElement")))
@protocol AppKotlinKAnnotatedElement
@required
@end


/**
 * @note annotations
 *   kotlin.SinceKotlin(version="1.1")
*/
__attribute__((swift_name("KotlinKClassifier")))
@protocol AppKotlinKClassifier
@required
@end

__attribute__((swift_name("KotlinKClass")))
@protocol AppKotlinKClass <AppKotlinKDeclarationContainer, AppKotlinKAnnotatedElement, AppKotlinKClassifier>
@required

/**
 * @note annotations
 *   kotlin.SinceKotlin(version="1.1")
*/
- (BOOL)isInstanceValue:(id _Nullable)value __attribute__((swift_name("isInstance(value:)")));
@property (readonly) NSString * _Nullable qualifiedName __attribute__((swift_name("qualifiedName")));
@property (readonly) NSString * _Nullable simpleName __attribute__((swift_name("simpleName")));
@end

__attribute__((swift_name("KotlinLazy")))
@protocol AppKotlinLazy
@required
- (BOOL)isInitialized __attribute__((swift_name("isInitialized()")));
@property (readonly) id _Nullable value __attribute__((swift_name("value")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("KotlinLazyThreadSafetyMode")))
@interface AppKotlinLazyThreadSafetyMode : AppKotlinEnum<AppKotlinLazyThreadSafetyMode *>
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
- (instancetype)initWithName:(NSString *)name ordinal:(int32_t)ordinal __attribute__((swift_name("init(name:ordinal:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
@property (class, readonly) AppKotlinLazyThreadSafetyMode *synchronized __attribute__((swift_name("synchronized")));
@property (class, readonly) AppKotlinLazyThreadSafetyMode *publication __attribute__((swift_name("publication")));
@property (class, readonly) AppKotlinLazyThreadSafetyMode *none __attribute__((swift_name("none")));
+ (AppKotlinArray<AppKotlinLazyThreadSafetyMode *> *)values __attribute__((swift_name("values()")));
@property (class, readonly) NSArray<AppKotlinLazyThreadSafetyMode *> *entries __attribute__((swift_name("entries")));
@end

__attribute__((swift_name("Koin_coreScopeCallback")))
@protocol AppKoin_coreScopeCallback
@required
- (void)onScopeCloseScope:(AppKoin_coreScope *)scope __attribute__((swift_name("onScopeClose(scope:)")));
@end

__attribute__((swift_name("Stately_concurrencyThreadLocalRef")))
@interface AppStately_concurrencyThreadLocalRef<T> : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (T _Nullable)get __attribute__((swift_name("get()")));
- (void)remove __attribute__((swift_name("remove()")));
- (void)setValue_:(T _Nullable)value __attribute__((swift_name("set(value:)")));
@end

__attribute__((swift_name("Koin_coreLogger")))
@interface AppKoin_coreLogger : AppBase
- (instancetype)initWithLevel:(AppKoin_coreLevel *)level __attribute__((swift_name("init(level:)"))) __attribute__((objc_designated_initializer));
- (void)debugMsg:(NSString *)msg __attribute__((swift_name("debug(msg:)")));
- (void)displayLevel:(AppKoin_coreLevel *)level msg:(NSString *)msg __attribute__((swift_name("display(level:msg:)")));
- (void)errorMsg:(NSString *)msg __attribute__((swift_name("error(msg:)")));
- (void)infoMsg:(NSString *)msg __attribute__((swift_name("info(msg:)")));
- (BOOL)isAtLvl:(AppKoin_coreLevel *)lvl __attribute__((swift_name("isAt(lvl:)")));
- (void)logLvl:(AppKoin_coreLevel *)lvl msg:(NSString *(^)(void))msg __attribute__((swift_name("log(lvl:msg:)")));
- (void)logLvl:(AppKoin_coreLevel *)lvl msg_:(NSString *)msg __attribute__((swift_name("log(lvl:msg_:)")));
- (void)warnMsg:(NSString *)msg __attribute__((swift_name("warn(msg:)")));
@property AppKoin_coreLevel *level __attribute__((swift_name("level")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreKoinApplication.Companion")))
@interface AppKoin_coreKoinApplicationCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppKoin_coreKoinApplicationCompanion *shared __attribute__((swift_name("shared")));
- (AppKoin_coreKoinApplication *)doInit __attribute__((swift_name("doInit()")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreLevel")))
@interface AppKoin_coreLevel : AppKotlinEnum<AppKoin_coreLevel *>
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
- (instancetype)initWithName:(NSString *)name ordinal:(int32_t)ordinal __attribute__((swift_name("init(name:ordinal:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
@property (class, readonly) AppKoin_coreLevel *debug __attribute__((swift_name("debug")));
@property (class, readonly) AppKoin_coreLevel *info __attribute__((swift_name("info")));
@property (class, readonly) AppKoin_coreLevel *warning __attribute__((swift_name("warning")));
@property (class, readonly) AppKoin_coreLevel *error __attribute__((swift_name("error")));
@property (class, readonly) AppKoin_coreLevel *none __attribute__((swift_name("none")));
+ (AppKotlinArray<AppKoin_coreLevel *> *)values __attribute__((swift_name("values()")));
@property (class, readonly) NSArray<AppKoin_coreLevel *> *entries __attribute__((swift_name("entries")));
@end

__attribute__((swift_name("KotlinCoroutineContextElement")))
@protocol AppKotlinCoroutineContextElement <AppKotlinCoroutineContext>
@required
@property (readonly) id<AppKotlinCoroutineContextKey> key __attribute__((swift_name("key")));
@end

__attribute__((swift_name("KotlinCoroutineContextKey")))
@protocol AppKotlinCoroutineContextKey
@required
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioByteString.Companion")))
@interface AppOkioByteStringCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppOkioByteStringCompanion *shared __attribute__((swift_name("shared")));
- (AppOkioByteString * _Nullable)decodeBase64:(NSString *)receiver __attribute__((swift_name("decodeBase64(_:)")));
- (AppOkioByteString *)decodeHex:(NSString *)receiver __attribute__((swift_name("decodeHex(_:)")));
- (AppOkioByteString *)encodeUtf8:(NSString *)receiver __attribute__((swift_name("encodeUtf8(_:)")));
- (AppOkioByteString *)ofData:(AppKotlinByteArray *)data __attribute__((swift_name("of(data:)")));
- (AppOkioByteString *)toByteString:(NSData *)receiver __attribute__((swift_name("toByteString(_:)")));
- (AppOkioByteString *)toByteString:(AppKotlinByteArray *)receiver offset:(int32_t)offset byteCount:(int32_t)byteCount __attribute__((swift_name("toByteString(_:offset:byteCount:)")));
@property (readonly) AppOkioByteString *EMPTY __attribute__((swift_name("EMPTY")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreCompositeEncoder")))
@protocol AppKotlinx_serialization_coreCompositeEncoder
@required
- (void)encodeBooleanElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(BOOL)value __attribute__((swift_name("encodeBooleanElement(descriptor:index:value:)")));
- (void)encodeByteElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(int8_t)value __attribute__((swift_name("encodeByteElement(descriptor:index:value:)")));
- (void)encodeCharElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(unichar)value __attribute__((swift_name("encodeCharElement(descriptor:index:value:)")));
- (void)encodeDoubleElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(double)value __attribute__((swift_name("encodeDoubleElement(descriptor:index:value:)")));
- (void)encodeFloatElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(float)value __attribute__((swift_name("encodeFloatElement(descriptor:index:value:)")));
- (id<AppKotlinx_serialization_coreEncoder>)encodeInlineElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("encodeInlineElement(descriptor:index:)")));
- (void)encodeIntElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(int32_t)value __attribute__((swift_name("encodeIntElement(descriptor:index:value:)")));
- (void)encodeLongElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(int64_t)value __attribute__((swift_name("encodeLongElement(descriptor:index:value:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (void)encodeNullableSerializableElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index serializer:(id<AppKotlinx_serialization_coreSerializationStrategy>)serializer value:(id _Nullable)value __attribute__((swift_name("encodeNullableSerializableElement(descriptor:index:serializer:value:)")));
- (void)encodeSerializableElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index serializer:(id<AppKotlinx_serialization_coreSerializationStrategy>)serializer value:(id _Nullable)value __attribute__((swift_name("encodeSerializableElement(descriptor:index:serializer:value:)")));
- (void)encodeShortElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(int16_t)value __attribute__((swift_name("encodeShortElement(descriptor:index:value:)")));
- (void)encodeStringElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index value:(NSString *)value __attribute__((swift_name("encodeStringElement(descriptor:index:value:)")));
- (void)endStructureDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("endStructure(descriptor:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (BOOL)shouldEncodeElementDefaultDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("shouldEncodeElementDefault(descriptor:index:)")));
@property (readonly) AppKotlinx_serialization_coreSerializersModule *serializersModule __attribute__((swift_name("serializersModule")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreSerializersModule")))
@interface AppKotlinx_serialization_coreSerializersModule : AppBase

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (void)dumpToCollector:(id<AppKotlinx_serialization_coreSerializersModuleCollector>)collector __attribute__((swift_name("dumpTo(collector:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (id<AppKotlinx_serialization_coreKSerializer> _Nullable)getContextualKClass:(id<AppKotlinKClass>)kClass typeArgumentsSerializers:(NSArray<id<AppKotlinx_serialization_coreKSerializer>> *)typeArgumentsSerializers __attribute__((swift_name("getContextual(kClass:typeArgumentsSerializers:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (id<AppKotlinx_serialization_coreSerializationStrategy> _Nullable)getPolymorphicBaseClass:(id<AppKotlinKClass>)baseClass value:(id)value __attribute__((swift_name("getPolymorphic(baseClass:value:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (id<AppKotlinx_serialization_coreDeserializationStrategy> _Nullable)getPolymorphicBaseClass:(id<AppKotlinKClass>)baseClass serializedClassName:(NSString * _Nullable)serializedClassName __attribute__((swift_name("getPolymorphic(baseClass:serializedClassName:)")));
@end

__attribute__((swift_name("KotlinAnnotation")))
@protocol AppKotlinAnnotation
@required
@end


/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
__attribute__((swift_name("Kotlinx_serialization_coreSerialKind")))
@interface AppKotlinx_serialization_coreSerialKind : AppBase
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@end

__attribute__((swift_name("Kotlinx_serialization_coreCompositeDecoder")))
@protocol AppKotlinx_serialization_coreCompositeDecoder
@required
- (BOOL)decodeBooleanElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeBooleanElement(descriptor:index:)")));
- (int8_t)decodeByteElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeByteElement(descriptor:index:)")));
- (unichar)decodeCharElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeCharElement(descriptor:index:)")));
- (int32_t)decodeCollectionSizeDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("decodeCollectionSize(descriptor:)")));
- (double)decodeDoubleElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeDoubleElement(descriptor:index:)")));
- (int32_t)decodeElementIndexDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("decodeElementIndex(descriptor:)")));
- (float)decodeFloatElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeFloatElement(descriptor:index:)")));
- (id<AppKotlinx_serialization_coreDecoder>)decodeInlineElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeInlineElement(descriptor:index:)")));
- (int32_t)decodeIntElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeIntElement(descriptor:index:)")));
- (int64_t)decodeLongElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeLongElement(descriptor:index:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (id _Nullable)decodeNullableSerializableElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index deserializer:(id<AppKotlinx_serialization_coreDeserializationStrategy>)deserializer previousValue:(id _Nullable)previousValue __attribute__((swift_name("decodeNullableSerializableElement(descriptor:index:deserializer:previousValue:)")));

/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
- (BOOL)decodeSequentially __attribute__((swift_name("decodeSequentially()")));
- (id _Nullable)decodeSerializableElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index deserializer:(id<AppKotlinx_serialization_coreDeserializationStrategy>)deserializer previousValue:(id _Nullable)previousValue __attribute__((swift_name("decodeSerializableElement(descriptor:index:deserializer:previousValue:)")));
- (int16_t)decodeShortElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeShortElement(descriptor:index:)")));
- (NSString *)decodeStringElementDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor index:(int32_t)index __attribute__((swift_name("decodeStringElement(descriptor:index:)")));
- (void)endStructureDescriptor:(id<AppKotlinx_serialization_coreSerialDescriptor>)descriptor __attribute__((swift_name("endStructure(descriptor:)")));
@property (readonly) AppKotlinx_serialization_coreSerializersModule *serializersModule __attribute__((swift_name("serializersModule")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("KotlinNothing")))
@interface AppKotlinNothing : AppBase
@end

__attribute__((swift_name("OkioTimeout")))
@interface AppOkioTimeout : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
@property (class, readonly, getter=companion) AppOkioTimeoutCompanion *companion __attribute__((swift_name("companion")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioBuffer")))
@interface AppOkioBuffer : AppBase <AppOkioBufferedSource, AppOkioBufferedSink>
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
- (void)clear __attribute__((swift_name("clear()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)closeAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("close_()")));
- (int64_t)completeSegmentByteCount __attribute__((swift_name("completeSegmentByteCount()")));
- (AppOkioBuffer *)doCopy __attribute__((swift_name("doCopy()")));
- (AppOkioBuffer *)doCopyToOut:(AppOkioBuffer *)out offset:(int64_t)offset __attribute__((swift_name("doCopyTo(out:offset:)")));
- (AppOkioBuffer *)doCopyToOut:(AppOkioBuffer *)out offset:(int64_t)offset byteCount:(int64_t)byteCount __attribute__((swift_name("doCopyTo(out:offset:byteCount:)")));
- (AppOkioBuffer *)emit __attribute__((swift_name("emit()")));
- (AppOkioBuffer *)emitCompleteSegments __attribute__((swift_name("emitCompleteSegments()")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (BOOL)exhausted __attribute__((swift_name("exhausted()")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)flushAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("flush()")));
- (int8_t)getPos:(int64_t)pos __attribute__((swift_name("get(pos:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (AppOkioByteString *)hmacSha1Key:(AppOkioByteString *)key __attribute__((swift_name("hmacSha1(key:)")));
- (AppOkioByteString *)hmacSha256Key:(AppOkioByteString *)key __attribute__((swift_name("hmacSha256(key:)")));
- (AppOkioByteString *)hmacSha512Key:(AppOkioByteString *)key __attribute__((swift_name("hmacSha512(key:)")));
- (int64_t)indexOfB:(int8_t)b __attribute__((swift_name("indexOf(b:)")));
- (int64_t)indexOfBytes:(AppOkioByteString *)bytes __attribute__((swift_name("indexOf(bytes:)")));
- (int64_t)indexOfB:(int8_t)b fromIndex:(int64_t)fromIndex __attribute__((swift_name("indexOf(b:fromIndex:)")));
- (int64_t)indexOfBytes:(AppOkioByteString *)bytes fromIndex:(int64_t)fromIndex __attribute__((swift_name("indexOf(bytes:fromIndex:)")));
- (int64_t)indexOfB:(int8_t)b fromIndex:(int64_t)fromIndex toIndex:(int64_t)toIndex __attribute__((swift_name("indexOf(b:fromIndex:toIndex:)")));
- (int64_t)indexOfElementTargetBytes:(AppOkioByteString *)targetBytes __attribute__((swift_name("indexOfElement(targetBytes:)")));
- (int64_t)indexOfElementTargetBytes:(AppOkioByteString *)targetBytes fromIndex:(int64_t)fromIndex __attribute__((swift_name("indexOfElement(targetBytes:fromIndex:)")));
- (AppOkioByteString *)md5 __attribute__((swift_name("md5()")));
- (id<AppOkioBufferedSource>)peek __attribute__((swift_name("peek()")));
- (BOOL)rangeEqualsOffset:(int64_t)offset bytes:(AppOkioByteString *)bytes __attribute__((swift_name("rangeEquals(offset:bytes:)")));
- (BOOL)rangeEqualsOffset:(int64_t)offset bytes:(AppOkioByteString *)bytes bytesOffset:(int32_t)bytesOffset byteCount:(int32_t)byteCount __attribute__((swift_name("rangeEquals(offset:bytes:bytesOffset:byteCount:)")));
- (int32_t)readSink:(AppKotlinByteArray *)sink __attribute__((swift_name("read(sink:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (int64_t)readSink:(AppOkioBuffer *)sink byteCount:(int64_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("read(sink:byteCount:)"))) __attribute__((swift_error(nonnull_error)));
- (int32_t)readSink:(AppKotlinByteArray *)sink offset:(int32_t)offset byteCount:(int32_t)byteCount __attribute__((swift_name("read(sink:offset:byteCount:)")));
- (int64_t)readAllSink:(id<AppOkioSink>)sink __attribute__((swift_name("readAll(sink:)")));
- (AppOkioBufferUnsafeCursor *)readAndWriteUnsafeUnsafeCursor:(AppOkioBufferUnsafeCursor *)unsafeCursor __attribute__((swift_name("readAndWriteUnsafe(unsafeCursor:)")));
- (int8_t)readByte __attribute__((swift_name("readByte()")));
- (AppKotlinByteArray *)readByteArray __attribute__((swift_name("readByteArray()")));
- (AppKotlinByteArray *)readByteArrayByteCount:(int64_t)byteCount __attribute__((swift_name("readByteArray(byteCount:)")));
- (AppOkioByteString *)readByteString __attribute__((swift_name("readByteString()")));
- (AppOkioByteString *)readByteStringByteCount:(int64_t)byteCount __attribute__((swift_name("readByteString(byteCount:)")));
- (int64_t)readDecimalLong __attribute__((swift_name("readDecimalLong()")));
- (void)readFullySink:(AppKotlinByteArray *)sink __attribute__((swift_name("readFully(sink:)")));
- (void)readFullySink:(AppOkioBuffer *)sink byteCount:(int64_t)byteCount __attribute__((swift_name("readFully(sink:byteCount:)")));
- (int64_t)readHexadecimalUnsignedLong __attribute__((swift_name("readHexadecimalUnsignedLong()")));
- (int32_t)readInt __attribute__((swift_name("readInt()")));
- (int32_t)readIntLe __attribute__((swift_name("readIntLe()")));
- (int64_t)readLong __attribute__((swift_name("readLong()")));
- (int64_t)readLongLe __attribute__((swift_name("readLongLe()")));
- (int16_t)readShort __attribute__((swift_name("readShort()")));
- (int16_t)readShortLe __attribute__((swift_name("readShortLe()")));
- (AppOkioBufferUnsafeCursor *)readUnsafeUnsafeCursor:(AppOkioBufferUnsafeCursor *)unsafeCursor __attribute__((swift_name("readUnsafe(unsafeCursor:)")));
- (NSString *)readUtf8 __attribute__((swift_name("readUtf8()")));
- (NSString *)readUtf8ByteCount:(int64_t)byteCount __attribute__((swift_name("readUtf8(byteCount:)")));
- (int32_t)readUtf8CodePoint __attribute__((swift_name("readUtf8CodePoint()")));
- (NSString * _Nullable)readUtf8Line __attribute__((swift_name("readUtf8Line()")));
- (NSString *)readUtf8LineStrict __attribute__((swift_name("readUtf8LineStrict()")));
- (NSString *)readUtf8LineStrictLimit:(int64_t)limit __attribute__((swift_name("readUtf8LineStrict(limit:)")));
- (BOOL)requestByteCount:(int64_t)byteCount __attribute__((swift_name("request(byteCount:)")));
- (void)requireByteCount:(int64_t)byteCount __attribute__((swift_name("require(byteCount:)")));
- (int32_t)selectOptions:(NSArray<AppOkioByteString *> *)options __attribute__((swift_name("select(options:)")));
- (AppOkioByteString *)sha1 __attribute__((swift_name("sha1()")));
- (AppOkioByteString *)sha256 __attribute__((swift_name("sha256()")));
- (AppOkioByteString *)sha512 __attribute__((swift_name("sha512()")));
- (void)skipByteCount:(int64_t)byteCount __attribute__((swift_name("skip(byteCount:)")));
- (AppOkioByteString *)snapshot __attribute__((swift_name("snapshot()")));
- (AppOkioByteString *)snapshotByteCount:(int32_t)byteCount __attribute__((swift_name("snapshot(byteCount:)")));
- (AppOkioTimeout *)timeout __attribute__((swift_name("timeout()")));
- (NSString *)description __attribute__((swift_name("description()")));
- (AppOkioBuffer *)writeSource:(AppKotlinByteArray *)source __attribute__((swift_name("write(source:)")));
- (AppOkioBuffer *)writeByteString:(AppOkioByteString *)byteString __attribute__((swift_name("write(byteString:)")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)writeSource:(AppOkioBuffer *)source byteCount:(int64_t)byteCount error:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("write(source:byteCount:)")));
- (AppOkioBuffer *)writeSource:(id<AppOkioSource>)source byteCount:(int64_t)byteCount __attribute__((swift_name("write(source:byteCount_:)")));
- (AppOkioBuffer *)writeSource:(AppKotlinByteArray *)source offset:(int32_t)offset byteCount:(int32_t)byteCount __attribute__((swift_name("write(source:offset:byteCount:)")));
- (AppOkioBuffer *)writeByteString:(AppOkioByteString *)byteString offset:(int32_t)offset byteCount:(int32_t)byteCount __attribute__((swift_name("write(byteString:offset:byteCount:)")));
- (int64_t)writeAllSource:(id<AppOkioSource>)source __attribute__((swift_name("writeAll(source:)")));
- (AppOkioBuffer *)writeByteB:(int32_t)b __attribute__((swift_name("writeByte(b:)")));
- (AppOkioBuffer *)writeDecimalLongV:(int64_t)v __attribute__((swift_name("writeDecimalLong(v:)")));
- (AppOkioBuffer *)writeHexadecimalUnsignedLongV:(int64_t)v __attribute__((swift_name("writeHexadecimalUnsignedLong(v:)")));
- (AppOkioBuffer *)writeIntI:(int32_t)i __attribute__((swift_name("writeInt(i:)")));
- (AppOkioBuffer *)writeIntLeI:(int32_t)i __attribute__((swift_name("writeIntLe(i:)")));
- (AppOkioBuffer *)writeLongV:(int64_t)v __attribute__((swift_name("writeLong(v:)")));
- (AppOkioBuffer *)writeLongLeV:(int64_t)v __attribute__((swift_name("writeLongLe(v:)")));
- (AppOkioBuffer *)writeShortS:(int32_t)s __attribute__((swift_name("writeShort(s:)")));
- (AppOkioBuffer *)writeShortLeS:(int32_t)s __attribute__((swift_name("writeShortLe(s:)")));
- (AppOkioBuffer *)writeUtf8String:(NSString *)string __attribute__((swift_name("writeUtf8(string:)")));
- (AppOkioBuffer *)writeUtf8String:(NSString *)string beginIndex:(int32_t)beginIndex endIndex:(int32_t)endIndex __attribute__((swift_name("writeUtf8(string:beginIndex:endIndex:)")));
- (AppOkioBuffer *)writeUtf8CodePointCodePoint:(int32_t)codePoint __attribute__((swift_name("writeUtf8CodePoint(codePoint:)")));
@property (readonly) AppOkioBuffer *buffer __attribute__((swift_name("buffer")));
@property (readonly) int64_t size __attribute__((swift_name("size")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioLock")))
@interface AppOkioLock : AppBase
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));
@property (class, readonly, getter=companion) AppOkioLockCompanion *companion __attribute__((swift_name("companion")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreBeanDefinition")))
@interface AppKoin_coreBeanDefinition<T> : AppBase
- (instancetype)initWithScopeQualifier:(id<AppKoin_coreQualifier>)scopeQualifier primaryType:(id<AppKotlinKClass>)primaryType qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier definition:(T _Nullable (^)(AppKoin_coreScope *, AppKoin_coreParametersHolder *))definition kind:(AppKoin_coreKind *)kind secondaryTypes:(NSArray<id<AppKotlinKClass>> *)secondaryTypes __attribute__((swift_name("init(scopeQualifier:primaryType:qualifier:definition:kind:secondaryTypes:)"))) __attribute__((objc_designated_initializer));
- (AppKoin_coreBeanDefinition<T> *)doCopyScopeQualifier:(id<AppKoin_coreQualifier>)scopeQualifier primaryType:(id<AppKotlinKClass>)primaryType qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier definition:(T _Nullable (^)(AppKoin_coreScope *, AppKoin_coreParametersHolder *))definition kind:(AppKoin_coreKind *)kind secondaryTypes:(NSArray<id<AppKotlinKClass>> *)secondaryTypes __attribute__((swift_name("doCopy(scopeQualifier:primaryType:qualifier:definition:kind:secondaryTypes:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (BOOL)hasTypeClazz:(id<AppKotlinKClass>)clazz __attribute__((swift_name("hasType(clazz:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (BOOL)isClazz:(id<AppKotlinKClass>)clazz qualifier:(id<AppKoin_coreQualifier> _Nullable)qualifier scopeDefinition:(id<AppKoin_coreQualifier>)scopeDefinition __attribute__((swift_name("is(clazz:qualifier:scopeDefinition:)")));
- (NSString *)description __attribute__((swift_name("description()")));
@property AppKoin_coreCallbacks<T> *callbacks __attribute__((swift_name("callbacks")));
@property (readonly) T _Nullable (^definition)(AppKoin_coreScope *, AppKoin_coreParametersHolder *) __attribute__((swift_name("definition")));
@property (readonly) AppKoin_coreKind *kind __attribute__((swift_name("kind")));
@property (readonly) id<AppKotlinKClass> primaryType __attribute__((swift_name("primaryType")));
@property id<AppKoin_coreQualifier> _Nullable qualifier __attribute__((swift_name("qualifier")));
@property (readonly) id<AppKoin_coreQualifier> scopeQualifier __attribute__((swift_name("scopeQualifier")));
@property NSArray<id<AppKotlinKClass>> *secondaryTypes __attribute__((swift_name("secondaryTypes")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreInstanceFactoryCompanion")))
@interface AppKoin_coreInstanceFactoryCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppKoin_coreInstanceFactoryCompanion *shared __attribute__((swift_name("shared")));
@property (readonly) NSString *ERROR_SEPARATOR __attribute__((swift_name("ERROR_SEPARATOR")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreInstanceContext")))
@interface AppKoin_coreInstanceContext : AppBase
- (instancetype)initWithLogger:(AppKoin_coreLogger *)logger scope:(AppKoin_coreScope *)scope parameters:(AppKoin_coreParametersHolder * _Nullable)parameters __attribute__((swift_name("init(logger:scope:parameters:)"))) __attribute__((objc_designated_initializer));
@property (readonly) AppKoin_coreLogger *logger __attribute__((swift_name("logger")));
@property (readonly) AppKoin_coreParametersHolder * _Nullable parameters __attribute__((swift_name("parameters")));
@property (readonly) AppKoin_coreScope *scope __attribute__((swift_name("scope")));
@end

__attribute__((swift_name("Koin_coreKoinComponent")))
@protocol AppKoin_coreKoinComponent
@required
- (AppKoin_coreKoin *)getKoin __attribute__((swift_name("getKoin()")));
@end

__attribute__((swift_name("Koin_coreKoinScopeComponent")))
@protocol AppKoin_coreKoinScopeComponent <AppKoin_coreKoinComponent>
@required
- (void)closeScope __attribute__((swift_name("closeScope()"))) __attribute__((deprecated("not used internaly anymore")));
@property (readonly) AppKoin_coreScope *scope __attribute__((swift_name("scope")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreExtensionManager")))
@interface AppKoin_coreExtensionManager : AppBase
- (instancetype)initWith_koin:(AppKoin_coreKoin *)_koin __attribute__((swift_name("init(_koin:)"))) __attribute__((objc_designated_initializer));
- (void)close __attribute__((swift_name("close()")));
- (id<AppKoin_coreKoinExtension>)getExtensionId:(NSString *)id __attribute__((swift_name("getExtension(id:)")));
- (id<AppKoin_coreKoinExtension> _Nullable)getExtensionOrNullId:(NSString *)id __attribute__((swift_name("getExtensionOrNull(id:)")));
- (void)registerExtensionId:(NSString *)id extension:(id<AppKoin_coreKoinExtension>)extension __attribute__((swift_name("registerExtension(id:extension:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreInstanceRegistry")))
@interface AppKoin_coreInstanceRegistry : AppBase
- (instancetype)initWith_koin:(AppKoin_coreKoin *)_koin __attribute__((swift_name("init(_koin:)"))) __attribute__((objc_designated_initializer));
- (void)saveMappingAllowOverride:(BOOL)allowOverride mapping:(NSString *)mapping factory:(AppKoin_coreInstanceFactory<id> *)factory logWarning:(BOOL)logWarning __attribute__((swift_name("saveMapping(allowOverride:mapping:factory:logWarning:)")));
- (int32_t)size __attribute__((swift_name("size()")));
@property (readonly) AppKoin_coreKoin *_koin __attribute__((swift_name("_koin")));
@property (readonly) NSDictionary<NSString *, AppKoin_coreInstanceFactory<id> *> *instances __attribute__((swift_name("instances")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_corePropertyRegistry")))
@interface AppKoin_corePropertyRegistry : AppBase
- (instancetype)initWith_koin:(AppKoin_coreKoin *)_koin __attribute__((swift_name("init(_koin:)"))) __attribute__((objc_designated_initializer));
- (void)close __attribute__((swift_name("close()")));
- (void)deletePropertyKey:(NSString *)key __attribute__((swift_name("deleteProperty(key:)")));
- (id _Nullable)getPropertyKey:(NSString *)key __attribute__((swift_name("getProperty(key:)")));
- (void)savePropertiesProperties:(NSDictionary<NSString *, id> *)properties __attribute__((swift_name("saveProperties(properties:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreScopeRegistry")))
@interface AppKoin_coreScopeRegistry : AppBase
- (instancetype)initWith_koin:(AppKoin_coreKoin *)_koin __attribute__((swift_name("init(_koin:)"))) __attribute__((objc_designated_initializer));
@property (class, readonly, getter=companion) AppKoin_coreScopeRegistryCompanion *companion __attribute__((swift_name("companion")));
- (void)loadScopesModules:(NSSet<AppKoin_coreModule *> *)modules __attribute__((swift_name("loadScopes(modules:)")));
@property (readonly) AppKoin_coreScope *rootScope __attribute__((swift_name("rootScope")));
@property (readonly) NSSet<id<AppKoin_coreQualifier>> *scopeDefinitions __attribute__((swift_name("scopeDefinitions")));
@end


/**
 * @note annotations
 *   kotlinx.serialization.ExperimentalSerializationApi
*/
__attribute__((swift_name("Kotlinx_serialization_coreSerializersModuleCollector")))
@protocol AppKotlinx_serialization_coreSerializersModuleCollector
@required
- (void)contextualKClass:(id<AppKotlinKClass>)kClass provider:(id<AppKotlinx_serialization_coreKSerializer> (^)(NSArray<id<AppKotlinx_serialization_coreKSerializer>> *))provider __attribute__((swift_name("contextual(kClass:provider:)")));
- (void)contextualKClass:(id<AppKotlinKClass>)kClass serializer:(id<AppKotlinx_serialization_coreKSerializer>)serializer __attribute__((swift_name("contextual(kClass:serializer:)")));
- (void)polymorphicBaseClass:(id<AppKotlinKClass>)baseClass actualClass:(id<AppKotlinKClass>)actualClass actualSerializer:(id<AppKotlinx_serialization_coreKSerializer>)actualSerializer __attribute__((swift_name("polymorphic(baseClass:actualClass:actualSerializer:)")));
- (void)polymorphicDefaultBaseClass:(id<AppKotlinKClass>)baseClass defaultDeserializerProvider:(id<AppKotlinx_serialization_coreDeserializationStrategy> _Nullable (^)(NSString * _Nullable))defaultDeserializerProvider __attribute__((swift_name("polymorphicDefault(baseClass:defaultDeserializerProvider:)"))) __attribute__((deprecated("Deprecated in favor of function with more precise name: polymorphicDefaultDeserializer")));
- (void)polymorphicDefaultDeserializerBaseClass:(id<AppKotlinKClass>)baseClass defaultDeserializerProvider:(id<AppKotlinx_serialization_coreDeserializationStrategy> _Nullable (^)(NSString * _Nullable))defaultDeserializerProvider __attribute__((swift_name("polymorphicDefaultDeserializer(baseClass:defaultDeserializerProvider:)")));
- (void)polymorphicDefaultSerializerBaseClass:(id<AppKotlinKClass>)baseClass defaultSerializerProvider:(id<AppKotlinx_serialization_coreSerializationStrategy> _Nullable (^)(id))defaultSerializerProvider __attribute__((swift_name("polymorphicDefaultSerializer(baseClass:defaultSerializerProvider:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioTimeout.Companion")))
@interface AppOkioTimeoutCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppOkioTimeoutCompanion *shared __attribute__((swift_name("shared")));
@property (readonly) AppOkioTimeout *NONE __attribute__((swift_name("NONE")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioBuffer.UnsafeCursor")))
@interface AppOkioBufferUnsafeCursor : AppBase <AppOkioCloseable>
- (instancetype)init __attribute__((swift_name("init()"))) __attribute__((objc_designated_initializer));
+ (instancetype)new __attribute__((availability(swift, unavailable, message="use object initializers instead")));

/**
 * @note This method converts instances of IOException to errors.
 * Other uncaught Kotlin exceptions are fatal.
*/
- (BOOL)closeAndReturnError:(NSError * _Nullable * _Nullable)error __attribute__((swift_name("close_()")));
- (int64_t)expandBufferMinByteCount:(int32_t)minByteCount __attribute__((swift_name("expandBuffer(minByteCount:)")));
- (int32_t)next __attribute__((swift_name("next()")));
- (int64_t)resizeBufferNewSize:(int64_t)newSize __attribute__((swift_name("resizeBuffer(newSize:)")));
- (int32_t)seekOffset:(int64_t)offset __attribute__((swift_name("seek(offset:)")));
@property AppOkioBuffer * _Nullable buffer __attribute__((swift_name("buffer")));
@property AppKotlinByteArray * _Nullable data __attribute__((swift_name("data")));
@property int32_t end __attribute__((swift_name("end")));
@property int64_t offset __attribute__((swift_name("offset")));
@property BOOL readWrite __attribute__((swift_name("readWrite")));
@property int32_t start __attribute__((swift_name("start")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("OkioLock.Companion")))
@interface AppOkioLockCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppOkioLockCompanion *shared __attribute__((swift_name("shared")));
@property (readonly) AppOkioLock *instance __attribute__((swift_name("instance")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreKind")))
@interface AppKoin_coreKind : AppKotlinEnum<AppKoin_coreKind *>
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
- (instancetype)initWithName:(NSString *)name ordinal:(int32_t)ordinal __attribute__((swift_name("init(name:ordinal:)"))) __attribute__((objc_designated_initializer)) __attribute__((unavailable));
@property (class, readonly) AppKoin_coreKind *singleton __attribute__((swift_name("singleton")));
@property (class, readonly) AppKoin_coreKind *factory __attribute__((swift_name("factory")));
@property (class, readonly) AppKoin_coreKind *scoped __attribute__((swift_name("scoped")));
+ (AppKotlinArray<AppKoin_coreKind *> *)values __attribute__((swift_name("values()")));
@property (class, readonly) NSArray<AppKoin_coreKind *> *entries __attribute__((swift_name("entries")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreCallbacks")))
@interface AppKoin_coreCallbacks<T> : AppBase
- (instancetype)initWithOnClose:(void (^ _Nullable)(T _Nullable))onClose __attribute__((swift_name("init(onClose:)"))) __attribute__((objc_designated_initializer));
- (AppKoin_coreCallbacks<T> *)doCopyOnClose:(void (^ _Nullable)(T _Nullable))onClose __attribute__((swift_name("doCopy(onClose:)")));
- (BOOL)isEqual:(id _Nullable)other __attribute__((swift_name("isEqual(_:)")));
- (NSUInteger)hash __attribute__((swift_name("hash()")));
- (NSString *)description __attribute__((swift_name("description()")));
@property (readonly) void (^ _Nullable onClose)(T _Nullable) __attribute__((swift_name("onClose")));
@end

__attribute__((swift_name("Koin_coreKoinExtension")))
@protocol AppKoin_coreKoinExtension
@required
- (void)onClose __attribute__((swift_name("onClose()")));
- (void)onRegisterKoin:(AppKoin_coreKoin *)koin __attribute__((swift_name("onRegister(koin:)")));
@end

__attribute__((objc_subclassing_restricted))
__attribute__((swift_name("Koin_coreScopeRegistry.Companion")))
@interface AppKoin_coreScopeRegistryCompanion : AppBase
+ (instancetype)alloc __attribute__((unavailable));
+ (instancetype)allocWithZone:(struct _NSZone *)zone __attribute__((unavailable));
+ (instancetype)companion __attribute__((swift_name("init()")));
@property (class, readonly, getter=shared) AppKoin_coreScopeRegistryCompanion *shared __attribute__((swift_name("shared")));
@end

#pragma pop_macro("_Nullable_result")
#pragma clang diagnostic pop
NS_ASSUME_NONNULL_END
