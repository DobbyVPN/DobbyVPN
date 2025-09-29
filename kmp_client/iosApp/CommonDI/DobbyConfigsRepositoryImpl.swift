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

    override fun getConnectionURL(): String {
        return userDefaults.string(forKey: connectionURLKey) ?? ""
    }

    override fun setConnectionURL(connectionURL: String) {
        userDefaults.set(connectionURL, forKey: c)
    }

    override fun getConnectionConfig(): String {
        return userDefaults.string(forKey: connectionConfigKey) ?? ""
    }

    override fun setConnectionConfig(connectionConfig: String) {
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

    public func setServerPortOutline(newOutlineKey: String) {
        userDefaults.set(newOutlineKey, forKey: ServerPortOutlineKey)
    }
    
    public func getMethodPasswordOutline() -> String {
        return userDefaults.string(forKey: MethodPasswordOutlineKey) ?? ""
    }
    
    public func setMethodPasswordOutline(newOutlineKey: String) {
        userDefaults.set(newOutlineKey, forKey: MethodPasswordOutlineKey)
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
