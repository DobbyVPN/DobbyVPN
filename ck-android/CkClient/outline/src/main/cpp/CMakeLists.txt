# SPDX-License-Identifier: Apache-2.0
#
# Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.

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
