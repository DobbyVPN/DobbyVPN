package com.dobby.outline

class GoBackend {
    companion object {
        init {
            // Сначала загружаем Go-библиотеку, затем нашу JNI-обёртку
            System.loadLibrary("outline")
            System.loadLibrary("outline_jni")
        }

        /** Инициализирует устройство с переданной Shadowsocks-конфигурацией */
        @JvmStatic
        external fun newNativeDevice(config: String): Unit

        /**
         * Пишет данные в Go-устройство.
         *
         * @param data байты для записи
         * @param length сколько байт записать из массива (обычно data.size)
         * @return сколько байт действительно записано, или -1 при ошибке
         */
        @JvmStatic
        external fun writeNative(data: ByteArray, length: Int): Int

        /**
         * Читает данные из Go-устройства.
         *
         * @param out буфер для приёма (должен быть достаточного размера)
         * @param maxLen максимально читаемое количество байт (обычно out.size)
         * @return сколько байт прочитано, или -1 при ошибке
         */
        @JvmStatic
        external fun readNative(out: ByteArray, maxLen: Int): Int
    }
}
