package interop.georouting

interface GeoroutingLibrary {
    fun SetGeoRoutingConf(cidrs: String)
    fun ClearGeoRoutingConf()
}
