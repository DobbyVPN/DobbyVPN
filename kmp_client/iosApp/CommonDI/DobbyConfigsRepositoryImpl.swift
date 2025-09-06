import app
import Foundation

public class DobbyConfigsRepositoryImpl: DobbyConfigsRepository {
    
    static let shared = DobbyConfigsRepositoryImpl()
    
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!
    
    private let cloakConfigKey = "cloakConfigKey"
    private let isCloakEnabledKey = "isCloakEnabledKey"
    private let outlineConfigKey = "outlineConfigKey"
    private let isOutlineEnabledKey = "isOutlineEnabledKey"
    
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
    
    public func getOutlineKey() -> String {
        return userDefaults.string(forKey: outlineConfigKey) ?? ""
    }
    
    public func setOutlineKey(newOutlineKey: String) {
        userDefaults.set(newOutlineKey, forKey: outlineConfigKey)
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
