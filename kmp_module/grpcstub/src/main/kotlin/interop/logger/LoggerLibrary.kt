package interop.logger

interface LoggerLibrary {
    fun InitLogger(path: String)
    fun InitTelemetry(endpoint: String, token: String)
    fun StopTelemetry()
    fun SetupTelemetryAttributes(config: String)
}
