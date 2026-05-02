import app

public final class HealthCheckImpl: HealthCheck {
    func getConnectionState() -> VpnConnectionState {
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

    func initHealthCheck() {
        Cloak_outlineInitHealthCheck()
    }

    func startHealthCheck() {
        Cloak_outlineStartHealthCheck()
    }

    func stopHealthCheck() {
        Cloak_outlineStopHealthCheck()
    }
}
