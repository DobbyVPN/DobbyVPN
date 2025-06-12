package com.dobby.outline

import android.util.Log
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class OutlineGo {
    companion object {
        private const val TAG = "OutlineGo"

        @Volatile
        private var isLibrariesLoaded = false
        private val loadingLock = Object()

        /**
         * Загружает библиотеки асинхронно
         * @return true если библиотеки успешно загружены
         */
        suspend fun loadLibraries(): Boolean = withContext(Dispatchers.IO) {
            synchronized(loadingLock) {
                if (isLibrariesLoaded) {
                    return@withContext true
                }

                try {
                    // Сначала загружаем Go-библиотеку, затем нашу JNI-обёртку
                    System.loadLibrary("outline")
                    System.loadLibrary("outline_jni")
                    isLibrariesLoaded = true
                    Log.d(TAG, "Libraries loaded successfully")
                    true
                } catch (e: UnsatisfiedLinkError) {
                    Log.e(TAG, "Failed to load libraries", e)
                    false
                }
            }
        }

        /**
         * Проверяет, загружены ли библиотеки
         * @throws IllegalStateException если библиотеки не загружены
         */
        @Throws(IllegalStateException::class)
        fun ensureLibrariesLoaded() {
            if (!isLibrariesLoaded) {
                throw IllegalStateException("Libraries not loaded. Call loadLibraries() first")
            }
        }

        /**
         * Инициализирует устройство с переданной Shadowsocks-конфигурацией
         * @throws IllegalStateException если библиотеки не загружены
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun newOutlineDevice(config: String): Unit

        /**
         * Пишет данные в Go-устройство.
         *
         * @param data байты для записи
         * @param length сколько байт записать из массива (обычно data.size)
         * @return сколько байт действительно записано, или -1 при ошибке
         * @throws IllegalStateException если библиотеки не загружены
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun write(data: ByteArray, length: Int): Int

        /**
         * Читает данные из Go-устройства.
         *
         * @param out буфер для приёма (должен быть достаточного размера)
         * @param maxLen максимально читаемое количество байт (обычно out.size)
         * @return сколько байт прочитано, или -1 при ошибке
         * @throws IllegalStateException если библиотеки не загружены
         */
        @JvmStatic
        @Throws(IllegalStateException::class)
        external fun read(out: ByteArray, maxLen: Int): Int

        /**
         * Безопасный вызов newOutlineDevice с проверкой загрузки библиотек
         */
        suspend fun safeNewOutlineDevice(config: String): Boolean = withContext(Dispatchers.IO) {
            try {
                if (!isLibrariesLoaded && !loadLibraries()) {
                    return@withContext false
                }
                newOutlineDevice(config)
                true
            } catch (e: Exception) {
                Log.e(TAG, "Failed to create outline device", e)
                false
            }
        }

        /**
         * Безопасный вызов write с проверкой загрузки библиотек
         */
        suspend fun safeWrite(data: ByteArray, length: Int): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                write(data, length)
            } catch (e: Exception) {
                Log.e(TAG, "Write failed", e)
                -1
            }
        }

        /**
         * Безопасный вызов read с проверкой загрузки библиотек
         */
        suspend fun safeRead(out: ByteArray, maxLen: Int): Int = withContext(Dispatchers.IO) {
            try {
                ensureLibrariesLoaded()
                read(out, maxLen)
            } catch (e: Exception) {
                Log.e(TAG, "Read failed", e)
                -1
            }
        }
    }
}