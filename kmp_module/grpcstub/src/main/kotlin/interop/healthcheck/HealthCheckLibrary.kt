package interop.healthcheck

interface HealthCheckLibrary {
    fun CouldStart(): Boolean
    fun CheckServerAlive(address: String, port: Int): Int
}
