import app
import MyLibrary

public class HealthCheckManagerImpl: HealthCheckManager {
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

    public func doInitHealthCheck() {
        Cloak_outlineInitHealthCheck()
    }

    public func startHealthCheck() {
        Cloak_outlineStartHealthCheck()
    }

    public func stopHealthCheck() {
        Cloak_outlineStopHealthCheck()
    }
}
