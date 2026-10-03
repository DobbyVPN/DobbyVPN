import Foundation
import Security

// These test-local declarations shadow Security's entry points when compiled
// beside the actual store. No production branch or dependency wrapper is needed.
private enum Keychain {
    static var values: [String: Data] = [:]
    static var collision = false
    static var retryStatus = errSecSuccess
    static var updates = 0
    static var messages: [String] = []

    static func account(_ query: CFDictionary) -> String {
        (query as NSDictionary)[kSecAttrAccount] as! String
    }
}

func SecItemUpdate(_ query: CFDictionary, _ attributes: CFDictionary) -> OSStatus {
    Keychain.updates += 1
    let key = Keychain.account(query)
    guard Keychain.values[key] != nil else { return errSecItemNotFound }
    guard Keychain.retryStatus == errSecSuccess else { return Keychain.retryStatus }
    Keychain.values[key] = (attributes as NSDictionary)[kSecValueData] as? Data
    return errSecSuccess
}

func SecItemAdd(_ query: CFDictionary, _ result: UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus {
    let key = Keychain.account(query)
    if Keychain.collision {
        Keychain.values[key] = Data("concurrent value".utf8)
        return errSecDuplicateItem
    }
    Keychain.values[key] = (query as NSDictionary)[kSecValueData] as? Data
    return errSecSuccess
}

func SecItemCopyMatching(_ query: CFDictionary, _ result: UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus {
    guard let value = Keychain.values[Keychain.account(query)] else { return errSecItemNotFound }
    result?.pointee = value as CFData
    return errSecSuccess
}

func SecItemDelete(_ query: CFDictionary) -> OSStatus {
    Keychain.values.removeValue(forKey: Keychain.account(query)) == nil ? errSecItemNotFound : errSecSuccess
}

enum IOSAppCompositionRoot {
    static let logsRepository = TestLog()
}

struct TestLog {
    func writeLog(level: String, log: String) {
        Keychain.messages.append("\(level) \(log)")
    }
}

@main
enum KeychainCollisionTest {
    static func main() {
        let store = SharedKeychainSecretStore.shared
        let requested = Data("requested value".utf8)
        Keychain.collision = true
        precondition(store.set(requested, for: "collision"))
        precondition(store.data(for: "collision") == requested, "collision retained another writer's value")
        precondition(Keychain.updates == 2, "add collision did not retry update")

        Keychain.retryStatus = errSecInteractionNotAllowed
        precondition(!store.set(requested, for: "failed-collision"), "failed retry reported success")
        precondition(store.string(for: "failed-collision") == "concurrent value")
        precondition(Keychain.messages.contains { $0.contains("update-after-add-collision") && $0.contains("\(errSecInteractionNotAllowed)") })

        Keychain.retryStatus = errSecSuccess
        Keychain.collision = false
        precondition(store.set("first", for: "ordinary"))
        precondition(store.set("replacement", for: "ordinary"))
        precondition(store.string(for: "ordinary") == "replacement")

        let suite = "vpn.dobby.keychain-test.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        defaults.set("legacy requested", forKey: "migration")
        Keychain.collision = true
        Keychain.retryStatus = errSecInteractionNotAllowed
        store.migrate(keys: ["migration"], from: defaults)
        precondition(defaults.string(forKey: "migration") == "legacy requested", "failed write removed original")
        Keychain.values.removeValue(forKey: "migration")
        Keychain.retryStatus = errSecSuccess
        store.migrate(keys: ["migration"], from: defaults)
        precondition(defaults.object(forKey: "migration") == nil)
        precondition(store.string(for: "migration") == "legacy requested")
        print("Keychain collision, failed retry, replacement and migration checks passed")
    }
}
