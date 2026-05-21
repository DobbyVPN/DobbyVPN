package interop.xray

interface XrayLibrary {
    fun GetXrayLastError(): String

    /**
     * @return 0 on success, non-zero on failure.
     */
    fun StartXray(config: String): Int

    fun StopXray()
}

