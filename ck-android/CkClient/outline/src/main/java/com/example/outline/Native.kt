package com.example.outline

object Native {
    init {
        System.loadLibrary("outline")      // liboutline.so
        System.loadLibrary("outline_jni")  // outline_jni.so
    }

    @JvmStatic external fun newNativeDevice(config: String): Unit
    @JvmStatic external fun writeNative(data: ByteArray, length: Int): Int
    @JvmStatic external fun readNative(out: ByteArray, maxLen: Int): Int
}