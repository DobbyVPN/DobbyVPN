import MyLibrary

public final class HealthCheckManagerImpl: HealthCheckManager {
    public func getConnectionState() -> VpnConnectionState {
        let state = Cloak_outlineGetConnectionState()

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

    public func initHealthCheck() {
        Cloak_outlineInitHealthCheck()
    }

    public func start() {
        Cloak_outlineStartHealthCheck()
    }

    public func stop() {
        Cloak_outlineStopHealthCheck()
    }
}
