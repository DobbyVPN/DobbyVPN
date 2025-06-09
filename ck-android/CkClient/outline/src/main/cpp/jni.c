/* SPDX-License-Identifier: Apache-2.0
 *
 * JNI-обёртка для liboutline.so
 */

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "liboutline.h"

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_newOutlineDevice(JNIEnv *env, jclass clazz, jstring jConfig)
{
const char *config_str = (*env)->GetStringUTFChars(env, jConfig, NULL);
// Вызываем Go-экспорт
NewOutlineDevice((char*)config_str);
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
