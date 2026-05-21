/* SPDX-License-Identifier: Apache-2.0 */

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "liboutline.h"

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

    jboolean success = (*env)->CallBooleanMethod(env, g_vpn_service_obj, g_protect_mid, (jint)fd);

    return success ? 1 : 0;
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_registerVpnService(JNIEnv *env, jclass clazz, jobject vpn_service) {
    if (g_vpn_service_obj != NULL) {
        (*env)->DeleteGlobalRef(env, g_vpn_service_obj);
    }

    g_vpn_service_obj = (*env)->NewGlobalRef(env, vpn_service);

    jclass vpn_cls = (*env)->FindClass(env, "android/net/VpnService");
    if (vpn_cls == NULL) {
        return;
    }

    g_protect_mid = (*env)->GetMethodID(env, vpn_cls, "protect", "(I)Z");

    (*env)->DeleteLocalRef(env, vpn_cls);
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_newOutlineClient(JNIEnv *env, jclass clazz, jstring jConfig, jint fd)
{
    const char *config_str = (*env)->GetStringUTFChars(env, jConfig, NULL);
    NewOutlineClient((char*)config_str, fd);
    (*env)->ReleaseStringUTFChars(env, jConfig, config_str);
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_outlineConnect(JNIEnv *env, jclass clazz)
{
    return OutlineConnect();
}

JNIEXPORT jstring JNICALL
Java_com_dobby_outline_OutlineGo_getLastError(JNIEnv *env, jclass clazz)
{
    char* err = GetLastError();
    if (err == NULL) {
        return NULL;
    }
    jstring result = (*env)->NewStringUTF(env, err);
    free(err);
    return result;
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_outlineDisconnect(JNIEnv *env, jclass clazz)
{
    OutlineDisconnect();
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_startCloakClient(JNIEnv *env, jclass clazz,
                                                  jstring jLocalHost, jstring jLocalPort,
                                                  jstring jConf, jboolean udp)
{
    const char *localHost = (*env)->GetStringUTFChars(env, jLocalHost, NULL);
    size_t localHost_len = (*env)->GetStringUTFLength(env, jLocalHost);
    const char *localPort = (*env)->GetStringUTFChars(env, jLocalPort, NULL);
    size_t localPort_len = (*env)->GetStringUTFLength(env, jLocalPort);
    const char *conf = (*env)->GetStringUTFChars(env, jConf, NULL);
    size_t conf_len = (*env)->GetStringUTFLength(env, jConf);

    StartCloakClient(
        (GoString){ .p = localHost, .n = localHost_len },
        (GoString){ .p = localPort, .n = localPort_len },
        (GoString){ .p = conf, .n = conf_len },
        udp
    );

    (*env)->ReleaseStringUTFChars(env, jLocalHost, localHost);
    (*env)->ReleaseStringUTFChars(env, jLocalPort, localPort);
    (*env)->ReleaseStringUTFChars(env, jConf, conf);
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_stopCloakClient(JNIEnv *env, jclass clazz)
{
    StopCloakClient();
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_initLogger(JNIEnv *env, jclass clazz,
                                            jstring jPath)
{
    const char *path = (*env)->GetStringUTFChars(env, jPath, NULL);
    size_t path_len = (*env)->GetStringUTFLength(env, jPath);

    InitLogger((GoString){ .p = path, .n = path_len });

    (*env)->ReleaseStringUTFChars(env, jPath, path);
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_checkServerAlive(JNIEnv *env, jclass clazz,
                                                  jstring jAddress, jint jPort)
{
    const char *address = (*env)->GetStringUTFChars(env, jAddress, NULL);
    size_t address_len = (*env)->GetStringUTFLength(env, jAddress);

    jint res = CheckServerAlive((GoString){ .p = address, .n = address_len }, jPort);

    (*env)->ReleaseStringUTFChars(env, jAddress, address);
    return res;
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_setGeoRoutingConf(JNIEnv *env, jclass clazz, jstring cidrs_c) {
    const char *cidrs = (*env)->GetStringUTFChars(env, cidrs_c, NULL);
    size_t cidrs_len = (*env)->GetStringUTFLength(env, cidrs_c);

    SetGeoRoutingConf((GoString){ .p = cidrs, .n = cidrs_len });

    (*env)->ReleaseStringUTFChars(env, cidrs_c, cidrs);
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_clearGeoRoutingConf(JNIEnv *env, jclass clazz) {
    ClearGeoRoutingConf();
}
