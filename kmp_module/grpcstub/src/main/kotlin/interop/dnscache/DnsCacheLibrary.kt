package interop.dnscache

interface DnsCacheLibrary {
    fun ClearDNSCache(): Unit
    fun SetDNSCacheEntries(entries: String, source: String): Int
}
