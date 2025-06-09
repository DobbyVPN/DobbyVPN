package com.dobby.outline

class GoBackend {
    /** Инициализирует устройство с переданной Shadowsocks-конфигурацией */
    external fun newNativeDevice(config: String): Unit

    /**
     * Пишет данные в Go-устройство.
     *
     * @param data байты для записи
     * @param length сколько байт записать из массива (обычно data.size)
     * @return сколько байт действительно записано, или -1 при ошибке
     */
    external fun writeNative(data: ByteArray, length: Int): Int

    /**
     * Читает данные из Go-устройства.
     *
     * @param out буфер для приёма (должен быть достаточного размера)
     * @param maxLen максимально читаемое количество байт (обычно out.size)
     * @return сколько байт прочитано, или -1 при ошибке
     */
    external fun readNative(out: ByteArray, maxLen: Int): Int

    companion object {
        init {
            // Сначала загружаем Go-библиотеку, затем нашу JNI-обёртку
            System.loadLibrary("outline")
//            System.loadLibrary("outline_jni")
        }
    }
}


//class GoBackend {
//    external fun awgTurnOn(ifname: String, tunFd: Int, settings: String): Int
//
//    external fun awgTurnOff(handle: Int)
//
//    external fun awgGetSocketV4(handle: Int): Int
//
//    external fun awgGetSocketV6(handle: Int): Int
//
//    external fun awgGetConfig(handle: Int): String
//
//    external fun awgVersion(): String
//
//    companion object {
//        init {
//            System.loadLibrary("wg-go")
//        }
//    }
//}