import app

public final class HealthCheckImpl: HealthCheck {
    public func GetConnectionState() -> VpnConnectionState {
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

    public func InitHealthCheck() {
        Cloak_outlineInitHealthCheck()
    }

    public func StartHealthCheck() {
        Cloak_outlineStartHealthCheck()
    }

    public func StopHealthCheck() {
        Cloak_outlineStopHealthCheck()
    }
}
