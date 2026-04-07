package interop.netcheck

interface NetCheckLibrary {
    fun NetCheck(configPath: String): String
    fun CancelNetCheck()
}
