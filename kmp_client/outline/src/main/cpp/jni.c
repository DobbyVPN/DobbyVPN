/* SPDX-License-Identifier: Apache-2.0
 *
 * JNI-обёртка для liboutline.so
 */

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "liboutline.h"

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_newOutlineClient(JNIEnv *env, jclass clazz, jstring jConfig)
{
const char *config_str = (*env)->GetStringUTFChars(env, jConfig, NULL);
// Вызываем Go-экспорт
NewOutlineClient((char*)config_str);
(*env)->ReleaseStringUTFChars(env, jConfig, config_str);
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_write(JNIEnv *env, jclass clazz,
                                       jbyteArray jBuf, jint length)
{
    jbyte *buf = (*env)->GetByteArrayElements(env, jBuf, NULL);
    // Вызываем Go-экспорт
    jint written = Write((char*)buf, length);
    // Не копируем данные обратно
    (*env)->ReleaseByteArrayElements(env, jBuf, buf, JNI_ABORT);
    return written;
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_read(JNIEnv *env, jclass clazz,
                                      jbyteArray jBuf, jint maxLen)
{
    jbyte *buf = (*env)->GetByteArrayElements(env, jBuf, NULL);
    // Вызываем Go-экспорт
    jint read = Read((char*)buf, maxLen);
    // Копируем данные обратно в Java-буфер
    (*env)->ReleaseByteArrayElements(env, jBuf, buf, 0);
    return read;
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_connect(JNIEnv *env, jclass clazz)
{
    // Вызываем Go-экспорт, возвращает 0 при успехе, -1 при ошибке
    return Connect();
}

JNIEXPORT jstring JNICALL
Java_com_dobby_outline_OutlineGo_getLastError(JNIEnv *env, jclass clazz)
{
    char* err = GetLastError();
    if (err == NULL) {
        return NULL;
    }
    jstring result = (*env)->NewStringUTF(env, err);
    free(err); // освобождаем память выделенную C.CString
    return result;
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_disconnect(JNIEnv *env, jclass clazz)
{
    // Вызываем Go-экспорт
    Disconnect();
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_startCloakClient(JNIEnv *env, jclass clazz,
                                                  jstring jLocalHost, jstring jLocalPort,
                                                  jstring jConf, jboolean udp)
{
    const char *localHost = (*env)->GetStringUTFChars(env, jLocalHost, NULL);
    const char *localPort = (*env)->GetStringUTFChars(env, jLocalPort, NULL);
    const char *conf = (*env)->GetStringUTFChars(env, jConf, NULL);
    // Вызываем Go-экспорт
    StartCloakClient(localHost, localPort, conf, udp);
    // Копируем данные обратно в Java-буфер
    (*env)->ReleaseStringUTFChars(env, jLocalHost, localHost);
    (*env)->ReleaseStringUTFChars(env, jLocalPort, localPort);
    (*env)->ReleaseStringUTFChars(env, jConf, conf);
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_stopCloakClient(JNIEnv *env, jclass clazz)
{
    // Вызываем Go-экспорт
    StopCloakClient();
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_initLogger(JNIEnv *env, jclass clazz,
                                                  jstring jPath)
{
    const char *path = (*env)->GetStringUTFChars(env, jPath, NULL);
    // Вызываем Go-экспорт
    InitLogger(path);
    // Копируем данные обратно в Java-буфер
    (*env)->ReleaseStringUTFChars(env, jPath, path);
}
