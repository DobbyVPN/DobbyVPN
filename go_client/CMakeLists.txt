cmake_minimum_required(VERSION 3.10)
project("outline_jni")

# Укажем, где лежит заголовок liboutline.h
include_directories("${CMAKE_CURRENT_SOURCE_DIR}/include")

# Импортируем предварительно собранную Go-библиотеку liboutline.so
add_library(outline
            SHARED
            IMPORTED)

# Пути к .so в папке jniLibs/<ABI>/
set_target_properties(outline PROPERTIES
    IMPORTED_LOCATION
      "${CMAKE_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}/liboutline.so"
)

# Собираем нашу JNI-обёртку
add_library(outline_jni
            SHARED
            outline_jni.cpp)

# Линкуем outline_jni → outline (.so) + log (Android logcat)
target_link_libraries(outline_jni
                      outline
                      log)