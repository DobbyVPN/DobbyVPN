package interop.healthcheck

interface HealthCheckLibrary {
    fun CouldStart(): Boolean
    fun GetConnectionState(): Int
    fun InitHealthCheck(config: String): Unit
    fun StartHealthCheck(): Unit
    fun StopHealthCheck(): Unit
}
