package interop.awg

interface AwgLibrary {
    fun StartAwg(key: String, config: String)
    fun StopAwg()
}
