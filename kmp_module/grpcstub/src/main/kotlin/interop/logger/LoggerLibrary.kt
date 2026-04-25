package interop.logger

interface LoggerLibrary {
    fun InitLogger(path: String)
    fun InitTelemetry(endpoint: String)
}
