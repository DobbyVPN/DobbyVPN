package interop.logger

interface LoggerLibrary {
    fun InitLogger(path: String)
    /** Legacy compatibility no-op; remote telemetry is permanently disabled. */
    fun InitTelemetry(endpoint: String, token: String)
    /** Legacy compatibility no-op; remote telemetry is permanently disabled. */
    fun StopTelemetry()
    /** Legacy compatibility no-op; remote telemetry is permanently disabled. */
    fun SetupTelemetryAttributes(config: String)
}
