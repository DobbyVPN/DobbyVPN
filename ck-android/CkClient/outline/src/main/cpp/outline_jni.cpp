#include <jni.h>
#include "liboutline.h"

// Замени com/example/outline/Native на свой package/class
extern "C" JNIEXPORT void JNICALL
Java_com_example_outline_Native_newOutlineDevice(JNIEnv* env, jclass /*clazz*/, jstring jConfig) {
    const char* config = env->GetStringUTFChars(jConfig, nullptr);
    // Вызываем Go-экспортированную функцию
    NewOutlineDevice((char*)config);
    env->ReleaseStringUTFChars(jConfig, config);
}

extern "C" JNIEXPORT jint JNICALL
Java_com_example_outline_Native_write(JNIEnv* env, jclass /*clazz*/, jbyteArray jBuf, jint length) {
    // Получаем указатель на данные байтового массива
    jbyte* buf = env->GetByteArrayElements(jBuf, nullptr);
    // Вызываем Go-функцию записи
    jint written = Write((char*)buf, length);
    // Освобождаем элементы (без копирования обратно в Java)
    env->ReleaseByteArrayElements(jBuf, buf, JNI_ABORT);
    return written;
}

extern "C" JNIEXPORT jint JNICALL
Java_com_example_outline_Native_read(JNIEnv* env, jclass /*clazz*/, jbyteArray jBuf, jint maxLen) {
    // Получаем указатель на буфер, куда будем записывать
    jbyte* buf = env->GetByteArrayElements(jBuf, nullptr);
    // Вызываем Go-функцию чтения
    jint read = Read((char*)buf, maxLen);
    // Копируем данные обратно в Java-буфер (mode 0)
    env->ReleaseByteArrayElements(jBuf, buf, 0);
    return read;
}