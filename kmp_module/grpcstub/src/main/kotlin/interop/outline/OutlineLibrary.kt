package interop.outline

interface OutlineLibrary {
    fun GetOutlineLastError(): String
    fun StartOutline(key: String): Int
    fun StopOutline()
}
