package interop.trusttunnel

interface TrustTunnelLibrary {
    fun GetTrustTunnelLastError(): String

    /**
     * @return 0 on success, non-zero on failure.
     */
    fun StartTrustTunnel(config: String): Int

    fun StopTrustTunnel()
}
