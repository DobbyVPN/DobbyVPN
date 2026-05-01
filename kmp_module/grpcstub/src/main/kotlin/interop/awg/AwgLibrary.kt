package interop.awg

interface AwgLibrary {
    fun StartAwg(config: String): Int
    fun StopAwg()
}
