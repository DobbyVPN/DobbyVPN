/* SPDX-License-Identifier: Apache-2.0
 *
 * JNI wrapper for liboutline.so
 * Provides a bridge between Java/Kotlin and Go-exported functions.
 */

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "liboutline.h"

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_newOutlineClient(JNIEnv *env, jclass clazz, jstring jConfig)
{
const char *config_str = (*env)->GetStringUTFChars(env, jConfig, NULL);
// Go Export
NewOutlineClient((char*)config_str);
(*env)->ReleaseStringUTFChars(env, jConfig, config_str);
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_write(JNIEnv *env, jclass clazz,
                                       jbyteArray jBuf, jint length)
{
    jbyte *buf = (*env)->GetByteArrayElements(env, jBuf, NULL);
    // Go Export
    jint written = Write((char*)buf, length);
    // Dont copy data back
    (*env)->ReleaseByteArrayElements(env, jBuf, buf, JNI_ABORT);
    return written;
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_read(JNIEnv *env, jclass clazz,
                                      jbyteArray jBuf, jint maxLen)
{
    jbyte *buf = (*env)->GetByteArrayElements(env, jBuf, NULL);
    // Go Export
    jint read = Read((char*)buf, maxLen);
    // Copy data back to Java buffer
    (*env)->ReleaseByteArrayElements(env, jBuf, buf, 0);
    return read;
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_connect(JNIEnv *env, jclass clazz)
{
    // Go Export, returns 0 on success, -1 on error
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
    free(err); // free memory allocated by C.CString
    return result;
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_disconnect(JNIEnv *env, jclass clazz)
{
    // Call Go-exported function to close the connection
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
    // Call Go-exported function to start the Cloak client
    StartCloakClient(localHost, localPort, conf, udp);
    // Release UTF-8 strings obtained from Java
    (*env)->ReleaseStringUTFChars(env, jLocalHost, localHost);
    (*env)->ReleaseStringUTFChars(env, jLocalPort, localPort);
    (*env)->ReleaseStringUTFChars(env, jConf, conf);
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_stopCloakClient(JNIEnv *env, jclass clazz)
{
    // Call Go-exported function to stop the Cloak client
    StopCloakClient();
}

JNIEXPORT void JNICALL
Java_com_dobby_outline_OutlineGo_initLogger(JNIEnv *env, jclass clazz,
                                            jstring jPath)
{
    const char *path = (*env)->GetStringUTFChars(env, jPath, NULL);
    // Call Go-exported function to initialize the logger
    InitLogger(path);
    // Release UTF-8 string obtained from Java
    (*env)->ReleaseStringUTFChars(env, jPath, path);
}

JNIEXPORT jint JNICALL
Java_com_dobby_outline_OutlineGo_checkServerAlive(JNIEnv *env, jclass clazz,
                                                  jstring jAddress, jint jPort)
{
    const char *address = (*env)->GetStringUTFChars(env, jAddress, NULL);
    // Call Go-exported function to check server availability
    jint res = CheckServerAlive(address, jPort);
    // Release UTF-8 string obtained from Java
    (*env)->ReleaseStringUTFChars(env, jAddress, address);
    return res;
}
