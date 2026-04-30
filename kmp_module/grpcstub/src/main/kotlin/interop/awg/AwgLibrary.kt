package interop.awg

interface AwgLibrary {
    fun StartAwg(key: String, config: String): Int
    fun StopAwg()
}
