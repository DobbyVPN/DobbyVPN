import app
import MyLibrary

public class HealthCheckManagerImpl: HealthCheckManager {
    private let configsRepository = DobbyConfigsRepositoryImpl.shared

    public func getConnectionState() -> VpnConnectionState {
        // Persisted health is intentionally not consulted here: it can outlive the extension
        // process and must never make the UI claim that a disconnected NE tunnel is connected.
        return IOSVpnConnectionAuthority.connectionState()
    }

    public func doInitHealthCheck() {
        configsRepository.setHealthCheckState(state: 0)
        configsRepository.setHealthCheckGeneration(IOSVpnConnectionAuthority.currentGeneration())
    }

    public func startHealthCheck() {
        configsRepository.setHealthCheckState(state: 1)
        configsRepository.setHealthCheckGeneration(IOSVpnConnectionAuthority.currentGeneration())
    }

    public func stopHealthCheck() {
        configsRepository.setHealthCheckState(state: 0)
        configsRepository.setHealthCheckGeneration(IOSVpnConnectionAuthority.currentGeneration())
    }

    public func measureTunnelProbeAverageLatencyMillis(timeoutMillis: Int64) -> Int64 {
        return Cloak_outlineMeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis)
    }
}
