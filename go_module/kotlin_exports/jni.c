#include <jni.h>
#include <stdlib.h>
#include <stdbool.h>
#include <string.h>

struct go_string { const char *str; long n; };
extern void StartCloakClient(struct go_string localHost, struct go_string localPort, struct go_string config, bool udp);
extern void StopCloakClient();
extern void SetGeoRoutingConf(struct go_string cidrs);
extern void ClearGeoRoutingConf();
extern int CheckServerAlive(struct go_string address, int port);
extern void InitLogger(struct go_string path);
extern char *GetLastError();
extern void NewOutlineClient(struct go_string config, int fd);
extern int OutlineConnect();
extern void OutlineDisconnect();
extern int AwgTurnOn(struct go_string ifname, int tun_fd, struct go_string settings);
extern void AwgTurnOff();
extern int AwgGetSocketV4();
extern int AwgGetSocketV6();

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

JNIEXPORT jint JNICALL Java_com_dobby_backend_GoBackend_awgTurnOn(JNIEnv *env, jclass c, jstring ifname, jint tun_fd, jstring settings)
{
	const char *ifname_str = (*env)->GetStringUTFChars(env, ifname, 0);
	size_t ifname_len = (*env)->GetStringUTFLength(env, ifname);
	const char *settings_str = (*env)->GetStringUTFChars(env, settings, 0);
	size_t settings_len = (*env)->GetStringUTFLength(env, settings);
	int ret = AwgTurnOn((struct go_string){
		.str = ifname_str,
		.n = ifname_len
	}, tun_fd, (struct go_string){
		.str = settings_str,
		.n = settings_len
	});
	(*env)->ReleaseStringUTFChars(env, ifname, ifname_str);
	(*env)->ReleaseStringUTFChars(env, settings, settings_str);
	return ret;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_awgTurnOff(JNIEnv *env, jclass c)
{
	AwgTurnOff();
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_GoBackend_awgGetSocketV4(JNIEnv *env, jclass c)
{
	return AwgGetSocketV4();
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_GoBackend_awgGetSocketV6(JNIEnv *env, jclass c)
{
	return AwgGetSocketV6();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_startCloakClient(JNIEnv *env, jclass c, jstring jLocalHost, jstring jLocalPort, jstring jConfig, jboolean udp)
{
    const char *localHost_str = (*env)->GetStringUTFChars(env, jLocalHost, NULL);
	size_t localHost_len = (*env)->GetStringUTFLength(env, jLocalHost);
    const char *localPort_str = (*env)->GetStringUTFChars(env, jLocalPort, NULL);
	size_t localPort_len = (*env)->GetStringUTFLength(env, jLocalPort);
    const char *config_str = (*env)->GetStringUTFChars(env, jConfig, NULL);
	size_t config_len = (*env)->GetStringUTFLength(env, jConfig);
    StartCloakClient((struct go_string){
		.str = localHost_str,
		.n = localHost_len
	},
	(struct go_string){
		.str = localPort_str,
		.n = localPort_len
	},
	(struct go_string){
		.str = config_str,
		.n = config_len
	}, udp);

    (*env)->ReleaseStringUTFChars(env, jLocalHost, localHost_str);
    (*env)->ReleaseStringUTFChars(env, jLocalPort, localPort_str);
    (*env)->ReleaseStringUTFChars(env, jConfig, config_str);
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_stopCloakClient(JNIEnv *env, jclass c)
{
    StopCloakClient();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_setGeoRoutingConf(JNIEnv *env, jclass c, jstring jCidrs)
{
	const char *cidrs_str = (*env)->GetStringUTFChars(env, jCidrs, NULL);
	size_t cidrs_len = (*env)->GetStringUTFLength(env, jCidrs);
    SetGeoRoutingConf((struct go_string){
		.str = cidrs_str,
		.n = cidrs_len
	});

    (*env)->ReleaseStringUTFChars(env, jCidrs, cidrs_str);
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_clearGeoRoutingConf(JNIEnv *env, jclass c)
{
    ClearGeoRoutingConf();
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_GoBackend_checkServerAlive(JNIEnv *env, jclass c, jstring jAddress, jint jPort)
{
	const char *address_str = (*env)->GetStringUTFChars(env, jAddress, NULL);
	size_t address_len = (*env)->GetStringUTFLength(env, jAddress);
    int result = CheckServerAlive((struct go_string){
		.str = address_str,
		.n = address_len
	}, jPort);

    (*env)->ReleaseStringUTFChars(env, jAddress, address_str);
	return result;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_initLogger(JNIEnv *env, jclass c, jstring jPath)
{
    const char *path_str = (*env)->GetStringUTFChars(env, jPath, NULL);
	size_t path_len = (*env)->GetStringUTFLength(env, jPath);
    InitLogger((struct go_string){
		.str = path_str,
		.n = path_len
	});
    (*env)->ReleaseStringUTFChars(env, jPath, path_str);
}

JNIEXPORT jstring JNICALL Java_com_dobby_backend_GoBackend_getLastError(JNIEnv *env, jclass c)
{
    char *result = GetLastError();
	jstring ret;
	if (!result)
		return NULL;
	ret = (*env)->NewStringUTF(env, result);
	free(result);
	return ret;
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_newOutlineClient(JNIEnv *env, jclass c, jstring jConfig, jint jFd)
{
    const char *config_str = (*env)->GetStringUTFChars(env, jConfig, NULL);
	size_t config_len = (*env)->GetStringUTFLength(env, jConfig);
    NewOutlineClient((struct go_string){
		.str = config_str,
		.n = config_len
	}, jFd);
    (*env)->ReleaseStringUTFChars(env, jConfig, config_str);
}

JNIEXPORT jint JNICALL Java_com_dobby_backend_GoBackend_outlineConnect(JNIEnv *env, jclass c)
{
    return OutlineConnect();
}

JNIEXPORT void JNICALL Java_com_dobby_backend_GoBackend_outlineDisconnect(JNIEnv *env, jclass c)
{
    OutlineDisconnect();
}
