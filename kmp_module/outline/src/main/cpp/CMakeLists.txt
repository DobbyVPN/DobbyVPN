cmake_minimum_required(VERSION 3.10)
project("outline_jni")

include_directories("${CMAKE_CURRENT_SOURCE_DIR}/include/${ANDROID_ABI}")
add_link_options("LINKER:--build-id=none")

add_library(outline_jni
        SHARED
        jni.c)

target_compile_options(outline_jni PRIVATE -fvisibility=default)

find_library(log-lib log)

target_link_libraries(outline_jni
        ${log-lib}
        "-loutline")

target_link_directories(outline_jni PRIVATE
        "${CMAKE_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}")

set_target_properties(outline_jni PROPERTIES
        BUILD_WITH_INSTALL_RPATH TRUE
        INSTALL_RPATH "$ORIGIN"
        INSTALL_RPATH_USE_LINK_PATH TRUE)

add_custom_command(TARGET outline_jni POST_BUILD
        COMMAND ${CMAKE_COMMAND} -E copy_if_different
        "${CMAKE_SOURCE_DIR}/../jniLibs/${ANDROID_ABI}/liboutline.so"
        "$<TARGET_FILE_DIR:outline_jni>/liboutline.so")
