Ниже полная пошаговая инструкция по интеграции вашей Go-библиотеки (`liboutline.so` + `liboutline.h`) и JNI-обёртки (`outline_jni.cpp`) в Android-проект, а также пример Kotlin-вызовов.

---

## 1. Организация файлов в Android-проекте

Предположим, у вас есть модуль `app`. Структура папок должна получиться примерно такая:

```
app/
└── src/
    └── main/
        ├── cpp/
        │   ├── CMakeLists.txt
        │   ├── include/
        │   │   └── liboutline.h
        │   └── outline_jni.cpp
        ├── jniLibs/
        │   ├── arm64-v8a/
        │   │   └── liboutline.so
        │   └── armeabi-v7a/
        │       └── liboutline.so
        └── java/
            └── com/
                └── example/
                    └── yourapp/
                        └── Native.kt
```

1. **`liboutline.so`**
   Скопируйте ваш `liboutline.so` в подпапки `jniLibs` для каждой ABI, переименовав в `liboutline.so`:

   ```
   app/src/main/jniLibs/arm64-v8a/liboutline.so
   app/src/main/jniLibs/armeabi-v7a/liboutline.so
   ```
2. **`liboutline.h`**
   Положите заголовок в папку include:

   ```
   app/src/main/cpp/include/liboutline.h
   ```
3. **`outline_jni.cpp`**
   Создайте в `app/src/main/cpp/outline_jni.cpp` файл со следующим содержимым (замените пакет `com_example_yourapp_Native` на ваш):

   ```cpp
   #include <jni.h>
   #include "liboutline.h"

   // package: com.example.yourapp, class: Native
   extern "C" JNIEXPORT void JNICALL
   Java_com_example_yourapp_Native_newNativeDevice(JNIEnv* env, jclass /*clazz*/, jstring jConfig) {
       const char* config = env->GetStringUTFChars(jConfig, nullptr);
       NewOutlineDevice((char*)config);
       env->ReleaseStringUTFChars(jConfig, config);
   }

   extern "C" JNIEXPORT jint JNICALL
   Java_com_example_yourapp_Native_writeNative(JNIEnv* env, jclass /*clazz*/,
                                               jbyteArray jBuf, jint length) {
       jbyte* buf = env->GetByteArrayElements(jBuf, nullptr);
       jint written = Write((char*)buf, length);
       env->ReleaseByteArrayElements(jBuf, buf, JNI_ABORT);
       return written;
   }

   extern "C" JNIEXPORT jint JNICALL
   Java_com_example_yourapp_Native_readNative(JNIEnv* env, jclass /*clazz*/,
                                              jbyteArray jBuf, jint maxLen) {
       jbyte* buf = env->GetByteArrayElements(jBuf, nullptr);
       jint read = Read((char*)buf, maxLen);
       env->ReleaseByteArrayElements(jBuf, buf, 0);
       return read;
   }
   ```

---

## 2. CMakeLists.txt

Создайте (или отредактируйте) `app/src/main/cpp/CMakeLists.txt`:

```cmake
cmake_minimum_required(VERSION 3.10)
project("outline_jni")

# Путь к заголовкам liboutline.h
include_directories("${CMAKE_CURRENT_SOURCE_DIR}/include")

# 1) Импортируем готовую Go-библиотеку
add_library(outline
            SHARED
            IMPORTED)

set_target_properties(outline PROPERTIES
    IMPORTED_LOCATION
      "${CMAKE_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}/liboutline.so"
)

# 2) Собираем JNI-обёртку
add_library(outline_jni
            SHARED
            outline_jni.cpp)

# 3) Линкуем:
#    outline_jni → outline (Go .so) + log (для __android_log*)
find_library(log-lib log)

target_link_libraries(outline_jni
                      outline
                      ${log-lib})
```

---

## 3. Gradle (Module: app)

В `app/build.gradle` подключите CMake:

```groovy
android {
    compileSdkVersion 33
    defaultConfig {
        applicationId "com.example.yourapp"
        minSdkVersion 19
        targetSdkVersion 33
        versionCode 1
        versionName "1.0"

        ndk {
            abiFilters "armeabi-v7a", "arm64-v8a"
        }

        externalNativeBuild {
            cmake {
                cppFlags "-std=c++11"
            }
        }
    }

    externalNativeBuild {
        cmake {
            path "src/main/cpp/CMakeLists.txt"
            version "3.10.2"
        }
    }
    // ...
}

dependencies {
    // ваши зависимости
}
```

После этого в Android Studio нажмите **Sync Project with Gradle Files**.

---

## 4. Kotlin-обёртка и вызовы

Создайте `Native.kt` в том же пакете, что и класс JNI-обёртки (`com.example.yourapp`):

```kotlin
package com.example.yourapp

object Native {
    init {
        System.loadLibrary("outline")      // liboutline.so
        System.loadLibrary("outline_jni")  // outline_jni.so
    }

    @JvmStatic external fun newNativeDevice(config: String): Unit
    @JvmStatic external fun writeNative(data: ByteArray, length: Int): Int
    @JvmStatic external fun readNative(out: ByteArray, maxLen: Int): Int
}
```

### Пример использования в Activity

```kotlin
package com.example.yourapp

import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity

class MainActivity : AppCompatActivity() {
    private val ssConfig = "ss://YOUR_SHADOWSOCKS_CONFIG"

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Инициализируем device
        Native.newNativeDevice(ssConfig)

        // Пишем данные
        val myData = "Hello".toByteArray()
        val written = Native.writeNative(myData, myData.size)

        // Читаем ответ
        val buf = ByteArray(4096)
        val read = Native.readNative(buf, buf.size)
        if (read > 0) {
            val response = buf.copyOf(read)
            // обработайте response...
        }
    }
}
```

---

Теперь после сборки Gradle:

* Go-библиотека (`liboutline.so`) автоматически попадёт в APK в папках `lib/armeabi-v7a/` и `lib/arm64-v8a/`.
* В рантайме вы сможете из Kotlin вызывать `Native.newNativeDevice(...)`, `writeNative(...)` и `readNative(...)`.
