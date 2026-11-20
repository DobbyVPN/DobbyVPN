import CryptoKit
import Foundation

/// Protects the legacy source URL and the Go-owned accepted configuration.
public final class DobbyConfigsRepositoryImpl {
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
            "sessionapi.v2.rawConfiguration",
        ]
        obsolete.forEach { userDefaults.removeObject(forKey: $0) }
        obsolete.forEach { secrets.remove($0) }
        secrets.migrate(keys: [sourceKey], from: userDefaults)
    }

    public func getConnectionURL() -> String { secrets.string(for: sourceKey) ?? "" }

    @discardableResult
    public func setConnectionURL(connectionURL: String) -> Bool {
        guard secrets.set(connectionURL, for: sourceKey) else { return false }
        userDefaults.removeObject(forKey: sourceKey)
        return true
    }

    @discardableResult
    public func clearConnectionURL() -> Bool {
        secrets.remove(sourceKey)
    }
}

extension DobbyConfigsRepositoryImpl {
    private func acceptedFile() throws -> URL {
        guard let directory = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroupIdentifier) else {
            throw CocoaError(.fileReadNoPermission)
        }
        return directory.appendingPathComponent("accepted-configuration.v1.enc")
    }

    public func loadAcceptedRecord() throws -> String {
        let url = try acceptedFile()
        let encrypted: Data
        do {
            let attributes = try FileManager.default.attributesOfItem(atPath: url.path)
            guard let size = attributes[.size] as? NSNumber, size.intValue <= 8 * 1_048_576 + 28,
                  attributes[.type] as? FileAttributeType == .typeRegular else { throw CocoaError(.fileReadCorruptFile) }
            encrypted = try Data(contentsOf: url)
        } catch let error as CocoaError where error.code == .fileReadNoSuchFile { return "" }
        let key = try secrets.acceptedConfigurationKey(create: false)
        let clear = try AES.GCM.open(AES.GCM.SealedBox(combined: encrypted), using: key)
        guard let value = String(data: clear, encoding: .utf8) else { throw CocoaError(.fileReadCorruptFile) }
        return value
    }

    public func saveAcceptedRecord(_ value: String) throws {
        let url = try acceptedFile()
        let key = try secrets.acceptedConfigurationKey(create: !FileManager.default.fileExists(atPath: url.path))
        guard let sealed = try AES.GCM.seal(Data(value.utf8), using: key).combined else { throw CocoaError(.fileWriteUnknown) }
        try sealed.write(to: url, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    public func clearAcceptedRecord() throws {
        do { try FileManager.default.removeItem(at: acceptedFile()) }
        catch let error as CocoaError where error.code == .fileNoSuchFile { return }
    }
}
