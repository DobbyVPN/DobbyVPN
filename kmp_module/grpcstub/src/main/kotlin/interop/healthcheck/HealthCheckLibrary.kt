package interop.healthcheck

interface HealthCheckLibrary {
    fun CouldStart(): Boolean
    fun GetConnectionState(): Int
    fun StartHealthCheck(): Unit
    fun StopHealthCheck(): Unit
}
