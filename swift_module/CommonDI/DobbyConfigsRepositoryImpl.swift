import app
import Foundation

/// Stores only the user-entered source URL. Go owns acquired bytes, parsing,
/// profile selection, telemetry policy, and protocol state.
public final class DobbyConfigsRepositoryImpl: DobbyConfigsRepository {
    public static let shared = DobbyConfigsRepositoryImpl()

    private let userDefaults = UserDefaults(suiteName: appGroupIdentifier) ?? UserDefaults.standard
    private let secrets = SharedKeychainSecretStore.shared
    private let sourceKey = "connectionURLKey"

    private init() {
        // Keep the URL while deleting obsolete cached configuration and
        // protocol/telemetry values. Never read deleted values into diagnostics.
        let obsolete = [
            "connectionConfigKey", "connectionProfilesKey", "activeConnectionProfileIndexKey",
            "vpnInterfaceKey", "geoRoutingConfKey", "telemetryEndpointKey",
            "telemetryApiTokenKey", "telemetryAttributesKey", "xrayConfigKey",
            "trustTunnelConfigKey", "ServerPortOutlineKey", "MethodPasswordOutlineKey",
            "PrefixOutlineKey", "TcpPathOutlineKey", "UdpPathOutlineKey",
        ]
        obsolete.forEach { userDefaults.removeObject(forKey: $0) }
        obsolete.forEach { secrets.remove($0) }
        secrets.migrate(keys: [sourceKey], from: userDefaults)
    }

    public func getConnectionURL() -> String { secrets.string(for: sourceKey) ?? "" }

    public func setConnectionURL(connectionURL: String) {
        precondition(secrets.set(connectionURL, for: sourceKey), "secure connection source write failed")
        userDefaults.removeObject(forKey: sourceKey)
    }
}
