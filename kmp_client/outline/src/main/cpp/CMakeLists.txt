# SPDX-License-Identifier: Apache-2.0
#
# Copyright © 2017-2023 WireGuard LLC. All Rights Reserved.

cmake_minimum_required(VERSION 3.10)
project("outline_jni")

# Путь к заголовкам liboutline.h
include_directories("${CMAKE_CURRENT_SOURCE_DIR}/include/${ANDROID_ABI}")
add_link_options("LINKER:--build-id=none")

# 1) Собираем JNI-обёртку
add_library(outline_jni
        SHARED
        jni.c)

# 2) Находим Android log библиотеку
find_library(log-lib log)

# 3) Линкуем с log и outline
# Используем относительный путь для линковки
target_link_libraries(outline_jni
        ${log-lib}
        "-loutline")

# 4) Указываем где искать liboutline.so во время линковки
target_link_directories(outline_jni PRIVATE
        "${CMAKE_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}")

# 5) Устанавливаем RPATH для поиска зависимостей в той же директории
set_target_properties(outline_jni PROPERTIES
        BUILD_WITH_INSTALL_RPATH TRUE
        INSTALL_RPATH "$ORIGIN"
        INSTALL_RPATH_USE_LINK_PATH TRUE)

# 6) Копируем liboutline.so в выходную директорию сборки
# Это гарантирует, что обе библиотеки будут в одной директории
add_custom_command(TARGET outline_jni POST_BUILD
        COMMAND ${CMAKE_COMMAND} -E copy_if_different
        "${CMAKE_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}/liboutline.so"
        "$<TARGET_FILE_DIR:outline_jni>/liboutline.so")