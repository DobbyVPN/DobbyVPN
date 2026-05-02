import app

public final class HealthCheckImpl: HealthCheck {
    func GetConnectionState() -> VpnConnectionState {
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

    func InitHealthCheck() {
        Cloak_outlineInitHealthCheck()
    }

    func StartHealthCheck() {
        Cloak_outlineStartHealthCheck()
    }

    func StopHealthCheck() {
        Cloak_outlineStopHealthCheck()
    }
}
