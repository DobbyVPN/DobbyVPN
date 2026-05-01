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
// extern GoInt32 CheckServerAlive(GoString address, GoInt32 port);
extern GoInt32 GetConnectionState();
extern void InitHealthCheck();
extern void StartHealthCheck();
extern void StopHealthCheck();
extern void InitLogger(GoString path);
extern char* GetLastError(void);
extern void NewOutlineClient(GoString config, GoInt32 fd);
extern GoInt32 OutlineConnect(void);
extern void OutlineDisconnect(void);

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
    // 1. Очищаем старую ссылку, если она была (защита от утечек при перезапуске)
    if (g_vpn_service_obj != NULL) {
        (*env)->DeleteGlobalRef(env, g_vpn_service_obj);
    }

    // 2. Создаем глобальную ссылку на переданный объект
    g_vpn_service_obj = (*env)->NewGlobalRef(env, vpn_service);

    // 3. Ищем КЛАСС VpnService напрямую в системе
    jclass vpn_cls = (*env)->FindClass(env, "android/net/VpnService");
    if (vpn_cls == NULL) {
        // Если класс не найден (теоретически невозможно на Android)
        return;
    }

    // 4. Ищем метод protect в найденном КЛАССЕ VpnService
    // Сигнатура "(I)Z" — принимает int, возвращает boolean
    g_protect_mid = (*env)->GetMethodID(env, vpn_cls, "protect", "(I)Z");

    if (g_protect_mid == NULL) {
        // Ошибка: метод не найден в классе VpnService
        // Проверь, что твой объект в Kotlin действительно наследует VpnService
    }

    // Освобождаем локальную ссылку на класс (GlobalRef для объекта остается!)
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

// JNIEXPORT jint JNICALL Java_com_dobby_backend_HealthCheckBackend_checkServerAlive(JNIEnv *env, jclass c, jstring jAddress, jint jPort)
// {
// 	const char *address_str = (*env)->GetStringUTFChars(env, jAddress, 0);
// 	size_t address_len = (*env)->GetStringUTFLength(env, jAddress);
//     int result = CheckServerAlive((GoString) {
// 		.p = address_str,
// 		.n = address_len
// 	}, jPort);

//     (*env)->ReleaseStringUTFChars(env, jAddress, address_str);
// 	return result;
// }

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

JNIEXPORT jstring JNICALL Java_com_dobby_backend_OutlineBackend_getLastError(JNIEnv *env, jclass c)
{
	jstring ret;
    char *result = GetLastError();
	if (!result)
		return NULL;
	ret = (*env)->NewStringUTF(env, result);
	free(result);
	return ret;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_OutlineBackend_newOutlineClient(JNIEnv *env, jclass c, jstring jConfig, jint jFd)
{
    const char *config_str = (*env)->GetStringUTFChars(env, jConfig, 0);
	size_t config_len = (*env)->GetStringUTFLength(env, jConfig);
    NewOutlineClient((GoString) {
		.p = config_str,
		.n = config_len
	}, jFd);
    (*env)->ReleaseStringUTFChars(env, jConfig, config_str);
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_OutlineBackend_outlineConnect(JNIEnv *env, jclass c)
{
    return OutlineConnect();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_OutlineBackend_outlineDisconnect(JNIEnv *env, jclass c)
{
    OutlineDisconnect();
}
