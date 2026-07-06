import app
import MyLibrary

public class HealthCheckManagerImpl: HealthCheckManager {
    private let configsRepository = DobbyConfigsRepositoryImpl.shared

    public func getConnectionState() -> VpnConnectionState {
        let sharedState = configsRepository.getHealthCheckState()
        let state = sharedState >= 0 ? sharedState : Cloak_outlineGetConnectionState()

        switch state {
        case 0:
            return .disconnected
        case 1:
            return .connecting
        case 2:
            return .connected
        default:
            // Defensive fallback (important for forward compatibility)
            return .disconnected
        }
    }

    public func doInitHealthCheck() {
        configsRepository.setHealthCheckState(state: 0)
    }

    public func startHealthCheck() {
        configsRepository.setHealthCheckState(state: 1)
    }

    public func stopHealthCheck() {
        configsRepository.setHealthCheckState(state: 0)
    }

    public func measureTunnelProbeAverageLatencyMillis(timeoutMillis: Int64) -> Int64 {
        return Cloak_outlineMeasureTunnelProbeAverageLatencyMillisWithTimeout(timeoutMillis)
    }
}
