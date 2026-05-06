#include <jni.h>
#include <stdlib.h>
#include <stdbool.h>
#include <string.h>
#include <stddef.h>


typedef unsigned char GoUint8;
typedef int GoInt32;

typedef struct { const char *p; ptrdiff_t n; } _GoString_;
extern size_t _GoStringLen(_GoString_ s);
extern const char *_GoStringPtr(_GoString_ s);
typedef _GoString_ GoString;

extern GoInt32 AwgTurnOn(GoString interfaceName, GoInt32 tunFd, GoString settings);
extern void AwgTurnOff(void);
extern GoInt32 AwgGetSocketV4(void);
extern GoInt32 AwgGetSocketV6(void);
extern void StartCloakClient(GoString localHost, GoString localPort, GoString config, GoUint8 udp);
extern void StopCloakClient(void);
extern void SetGeoRoutingConf(GoString cidrs);
extern void ClearGeoRoutingConf(void);
extern GoInt32 GetConnectionState();
extern void InitHealthCheck();
extern void StartHealthCheck();
extern void StopHealthCheck();
extern void InitLogger(GoString path);
extern void InitTelemetry(GoString endpoint);
extern char* NetCheck(GoString configPath);
extern void CancelNetCheck();
extern char* GetVpnLastError(void);
extern void NewVpnClient(GoString config, GoString protocol, GoInt32 fd);
extern GoInt32 VpnConnect(void);
extern void VpnDisconnect(void);

#define EXPORT __attribute__((visibility("default")))

static JavaVM *g_vm = NULL;
static jobject g_vpn_service_obj = NULL;
static jmethodID g_protect_mid = NULL;

JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM *vm, void *reserved) {
    g_vm = vm;
    return JNI_VERSION_1_6;
}

EXPORT int go_protect_socket(int fd) {
    if (g_vm == NULL || g_vpn_service_obj == NULL || g_protect_mid == NULL) {
        return 0;
    }

    JNIEnv *env;
    jint res = (*g_vm)->AttachCurrentThread(g_vm, &env, NULL);
    if (res != JNI_OK) return 0;

    // Call VpnService.protect(fd)
    jboolean success = (*env)->CallBooleanMethod(env, g_vpn_service_obj, g_protect_mid, (jint)fd);

    return success ? 1 : 0;
    return 0;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_registerVpnService(JNIEnv *env, jclass clazz, jobject vpn_service) {
    // 1. Release old reference if present (prevents leaks on restart)
    if (g_vpn_service_obj != NULL) {
        (*env)->DeleteGlobalRef(env, g_vpn_service_obj);
    }

    // 2. Create global reference to the passed object
    g_vpn_service_obj = (*env)->NewGlobalRef(env, vpn_service);

    // 3. Find VpnService CLASS directly in the system
    jclass vpn_cls = (*env)->FindClass(env, "android/net/VpnService");
    if (vpn_cls == NULL) {
        // Should not happen on Android
        return;
    }

    // 4. Find protect method in the VpnService CLASS
    // Signature "(I)Z" — takes int, returns boolean
    g_protect_mid = (*env)->GetMethodID(env, vpn_cls, "protect", "(I)Z");

    if (g_protect_mid == NULL) {
        // Error: protect method not found in VpnService class
        // Ensure your Kotlin object actually extends VpnService
    }

    // Release local class reference (GlobalRef for the object remains)
    (*env)->DeleteLocalRef(env, vpn_cls);
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_AwgBackend_awgTurnOn(JNIEnv *env, jclass c, jstring ifname, jint tun_fd, jstring settings)
{
	const char *ifname_str = (*env)->GetStringUTFChars(env, ifname, 0);
	size_t ifname_len = (*env)->GetStringUTFLength(env, ifname);
	const char *settings_str = (*env)->GetStringUTFChars(env, settings, 0);
	size_t settings_len = (*env)->GetStringUTFLength(env, settings);
	int ret = AwgTurnOn((GoString) {
		.p = ifname_str,
		.n = ifname_len
	}, tun_fd, (GoString) {
		.p = settings_str,
		.n = settings_len
	});
	(*env)->ReleaseStringUTFChars(env, ifname, ifname_str);
	(*env)->ReleaseStringUTFChars(env, settings, settings_str);
	return ret;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_AwgBackend_awgTurnOff(JNIEnv *env, jclass c)
{
	AwgTurnOff();
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_AwgBackend_awgGetSocketV4(JNIEnv *env, jclass c)
{
	return AwgGetSocketV4();
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_AwgBackend_awgGetSocketV6(JNIEnv *env, jclass c)
{
	return AwgGetSocketV6();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_CloakBackend_startCloakClient(JNIEnv *env, jclass c, jstring jLocalHost, jstring jLocalPort, jstring jConfig, jboolean udp)
{
    const char *localHost_str = (*env)->GetStringUTFChars(env, jLocalHost, 0);
	size_t localHost_len = (*env)->GetStringUTFLength(env, jLocalHost);
    const char *localPort_str = (*env)->GetStringUTFChars(env, jLocalPort, 0);
	size_t localPort_len = (*env)->GetStringUTFLength(env, jLocalPort);
    const char *config_str = (*env)->GetStringUTFChars(env, jConfig, 0);
	size_t config_len = (*env)->GetStringUTFLength(env, jConfig);
    StartCloakClient((GoString) {
		.p = localHost_str,
		.n = localHost_len
	},
	(GoString) {
		.p = localPort_str,
		.n = localPort_len
	},
	(GoString) {
		.p = config_str,
		.n = config_len
	}, udp);

    (*env)->ReleaseStringUTFChars(env, jLocalHost, localHost_str);
    (*env)->ReleaseStringUTFChars(env, jLocalPort, localPort_str);
    (*env)->ReleaseStringUTFChars(env, jConfig, config_str);
}

JNIEXPORT void JNICALL Java_com_dobby_backend_CloakBackend_stopCloakClient(JNIEnv *env, jclass c)
{
    StopCloakClient();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GeoRoutingBackend_setGeoRoutingConf(JNIEnv *env, jclass c, jstring jCidrs)
{
	const char *cidrs_str = (*env)->GetStringUTFChars(env, jCidrs, 0);
	size_t cidrs_len = (*env)->GetStringUTFLength(env, jCidrs);
    SetGeoRoutingConf((GoString) {
		.p = cidrs_str,
		.n = cidrs_len
	});

    (*env)->ReleaseStringUTFChars(env, jCidrs, cidrs_str);
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GeoRoutingBackend_clearGeoRoutingConf(JNIEnv *env, jclass c)
{
    ClearGeoRoutingConf();
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_HealthCheckBackend_getConnectionState(JNIEnv *env, jclass c)
{
	return GetConnectionState();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_HealthCheckBackend_initHealthCheck(JNIEnv *env, jclass c)
{
	InitHealthCheck();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_HealthCheckBackend_startHealthCheck(JNIEnv *env, jclass c)
{
	StartHealthCheck();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_HealthCheckBackend_stopHealthCheck(JNIEnv *env, jclass c)
{
	StopHealthCheck();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_LoggerBackend_initLogger(JNIEnv *env, jclass c, jstring jPath)
{
    const char *path_str = (*env)->GetStringUTFChars(env, jPath, 0);
	size_t path_len = (*env)->GetStringUTFLength(env, jPath);
    InitLogger((GoString) {
		.p = path_str,
		.n = path_len
	});
    (*env)->ReleaseStringUTFChars(env, jPath, path_str);
}

JNIEXPORT void JNICALL Java_com_dobby_backend_LoggerBackend_initTelemetry(JNIEnv *env, jclass c, jstring jEndpoint)
{
    const char *endpoint_str = (*env)->GetStringUTFChars(env, jEndpoint, 0);
	size_t endpoint_len = (*env)->GetStringUTFLength(env, jEndpoint);
    InitTelemetry((GoString) {
		.p = endpoint_str,
		.n = endpoint_len
	});
    (*env)->ReleaseStringUTFChars(env, jEndpoint, endpoint_str);
}

JNIEXPORT jstring JNICALL Java_com_dobby_backend_NetCheckBackend_netCheck(JNIEnv *env, jclass c, jstring jConfigPath)
{
	jstring ret;
    const char *config_path_str = (*env)->GetStringUTFChars(env, jConfigPath, 0);
	size_t config_path_len = (*env)->GetStringUTFLength(env, jConfigPath);
    char *result = NetCheck((GoString) {
		.p = config_path_str,
		.n = config_path_len
	});
    (*env)->ReleaseStringUTFChars(env, jConfigPath, config_path_str);
	if (!result)
		return NULL;
	ret = (*env)->NewStringUTF(env, result);
	free(result);
	return ret;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_NetCheckBackend_cancelNetCheck(JNIEnv *env, jclass c)
{
    CancelNetCheck();
}

JNIEXPORT jstring JNICALL Java_com_dobby_backend_VpnBackend_getLastError(JNIEnv *env, jclass c)
{
	jstring ret;
    char *result = GetVpnLastError();
	if (!result)
		return NULL;
	ret = (*env)->NewStringUTF(env, result);
	free(result);
	return ret;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_VpnBackend_newVpnClient(JNIEnv *env, jclass c, jstring jConfig, jstring jProtocol, jint jFd)
{
    const char *config_str = (*env)->GetStringUTFChars(env, jConfig, 0);
    const char *protocol_str = (*env)->GetStringUTFChars(env, jProtocol, 0);

    size_t config_len = (*env)->GetStringUTFLength(env, jConfig);
    const GoString go_config = (GoString) {	.p = config_str, .n = config_len };

    size_t protocol_len = (*env)->GetStringUTFLength(env, jProtocol);
    const GoString go_protocol = (GoString) { .p = protocol_str, .n = protocol_len };

    NewVpnClient(go_config, go_protocol, jFd);

    (*env)->ReleaseStringUTFChars(env, jConfig, config_str);
    (*env)->ReleaseStringUTFChars(env, jProtocol, protocol_str);
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_VpnBackend_vpnConnect(JNIEnv *env, jclass c)
{
    return VpnConnect();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_VpnBackend_vpnDisconnect(JNIEnv *env, jclass c)
{
    VpnDisconnect();
}
