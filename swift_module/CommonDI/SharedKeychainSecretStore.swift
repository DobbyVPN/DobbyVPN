import Foundation
import Security

/// Keychain storage shared by the containing app and packet-tunnel extension.
/// Legacy App Group values are removed only after SecItemAdd/Update succeeds.
public final class SharedKeychainSecretStore {
    public static let shared = SharedKeychainSecretStore()

    /// Shared by the app and packet-tunnel extension.  The mailbox is a
    /// one-shot encrypted Keychain item; it is never copied to UserDefaults or
    /// put into an app-message payload.
    public static let sessionConfigurationMailboxKey = "sessionapi.v2.configuration.mailbox"
    public static let sessionBridgeHMACKey = "sessionapi.v2.bridge.hmac"

    private let service = "vpn.dobby.app.config.v1"
    private let accessGroup: String?

    private init() {
        accessGroup = Bundle.main.object(forInfoDictionaryKey: "DobbyKeychainAccessGroup") as? String
    }

    public func data(for key: String) -> Data? {
        var query = baseQuery(key)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &result) == errSecSuccess else { return nil }
        return result as? Data
    }

    @discardableResult
    public func set(_ value: Data, for key: String) -> Bool {
        let query = baseQuery(key)
        let update: [String: Any] = [
            kSecValueData as String: value,
        ]
        let status = SecItemUpdate(query as CFDictionary, update as CFDictionary)
        if status == errSecSuccess { return true }
        guard status == errSecItemNotFound else { return false }
        var create = query
        create[kSecValueData as String] = value
        create[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let createStatus = SecItemAdd(create as CFDictionary, nil)
        // Two processes can initialize the shared per-install key at the same
        // time. The loser must reuse the key created by the winner rather than
        // treating the expected duplicate-item race as a storage failure.
        return createStatus == errSecSuccess || data(for: key) != nil
    }

    public func string(for key: String) -> String? {
        data(for: key).flatMap { String(data: $0, encoding: .utf8) }
    }

    @discardableResult
    public func set(_ value: String, for key: String) -> Bool {
        set(Data(value.utf8), for: key)
    }

    /// Returns the per-install bridge key, creating it only when the Keychain
    /// does not already contain one.  Both processes use the same access group
    /// and therefore observe one stable secret for the lifetime of an install.
    public func randomData(for key: String, byteCount: Int = 32) -> Data? {
        if let existing = data(for: key), existing.count == byteCount {
            return existing
        }
        var generated = Data(count: byteCount)
        let status = generated.withUnsafeMutableBytes { bytes in
            SecRandomCopyBytes(kSecRandomDefault, byteCount, bytes.baseAddress!)
        }
        guard status == errSecSuccess, set(generated, for: key) else { return nil }
        return data(for: key)
    }

    public func remove(_ key: String) {
        SecItemDelete(baseQuery(key) as CFDictionary)
    }

    public func migrate(keys: [String], from defaults: UserDefaults) {
        for key in keys {
            // If an older migration stored the Keychain value but was
            // interrupted before deleting UserDefaults, remove that plaintext
            // duplicate on the next idempotent pass. Reads never fall back.
            if data(for: key) != nil {
                defaults.removeObject(forKey: key)
                continue
            }
            guard let legacy = defaults.object(forKey: key) else { continue }
            let value: Data?
            if let data = legacy as? Data {
                value = data
            } else if let string = legacy as? String {
                value = Data(string.utf8)
            } else {
                value = nil
            }
            if let value, set(value, for: key) {
                defaults.removeObject(forKey: key)
            }
        }
    }

    private func baseQuery(_ key: String) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key,
        ]
        if let accessGroup, !accessGroup.isEmpty {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }
}
