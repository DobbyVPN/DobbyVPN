package interop.healthcheck

interface HealthCheckLibrary {
    fun CouldStart(): Boolean
    fun GetConnectionState(): Int
    fun InitHealthCheck(): Unit
    fun StartHealthCheck(): Unit
    fun StopHealthCheck(): Unit
    fun MeasureTunnelProbeAverageLatencyMillis(timeoutMillis: Long): Long
}
