import app
import Foundation

public class DobbyConfigsRepositoryImpl: DobbyConfigsRepository {
    static let shared = DobbyConfigsRepositoryImpl()
    
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!
    
    private let cloakConfigKey = "cloakConfigKey"
    private let isCloakEnabledKey = "isCloakEnabledKey"
    private let MethodPasswordOutlineKey = "MethodPasswordOutlineKey"
    private let ServerPortOutlineKey = "ServerPortOutlineKey"
    private let isOutlineEnabledKey = "isOutlineEnabledKey"
    private let connectionURLKey = "connectionURLKey"
    private let connectionConfigKey = "connectionConfigKey"

    public func getConnectionURL() -> String {
        return userDefaults.string(forKey: connectionURLKey) ?? ""
    }

    public func setConnectionURL(connectionURL: String) {
        userDefaults.set(connectionURL, forKey: connectionURLKey)
    }

    public func getConnectionConfig() -> String {
        return userDefaults.string(forKey: connectionConfigKey) ?? ""
    }

    public func setConnectionConfig(connectionConfig: String) {
        userDefaults.set(connectionConfig, forKey: connectionConfigKey)
    }
    
    public func getCloakConfig() -> String {
        return userDefaults.string(forKey: cloakConfigKey) ?? ""
    }
    
    public func setCloakConfig(newConfig: String) {
        userDefaults.set(newConfig, forKey: cloakConfigKey)
    }
    
    public func getIsCloakEnabled() -> Bool {
        return userDefaults.bool(forKey: isCloakEnabledKey)
    }

    public func setIsCloakEnabled(isCloakEnabled: Bool) {
        userDefaults.set(isCloakEnabled, forKey: isCloakEnabledKey)
    }

    public func getServerPortOutline() -> String {
        return userDefaults.string(forKey: ServerPortOutlineKey) ?? ""
    }

    public func setServerPortOutline(newConfig: String) {
        userDefaults.set(newConfig, forKey: ServerPortOutlineKey)
    }
    
    public func getMethodPasswordOutline() -> String {
        return userDefaults.string(forKey: MethodPasswordOutlineKey) ?? ""
    }
    
    public func setMethodPasswordOutline(newConfig: String) {
        userDefaults.set(newConfig, forKey: MethodPasswordOutlineKey)
    }
    
    public func getIsOutlineEnabled() -> Bool {
        return userDefaults.bool(forKey: isOutlineEnabledKey)
    }

    
    public func setIsOutlineEnabled(isOutlineEnabled: Bool) {
        userDefaults.set(isOutlineEnabled, forKey: isOutlineEnabledKey)
    }
    
    public func getAwgConfig() -> String {
        return ""
    }
    
    public func getIsAmneziaWGEnabled() -> Bool {
        return false
    }
    
    public func getVpnInterface() -> VpnInterface {
        return VpnInterface.cloakOutline
    }
    
    public func setAwgConfig(newConfig: String?) {}
    
    public func setIsAmneziaWGEnabled(isAmneziaWGEnabled: Bool) {}
    
    public func setVpnInterface(vpnInterface: VpnInterface) {}
}
