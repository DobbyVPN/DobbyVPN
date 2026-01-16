import app
import Foundation

public class DobbyConfigsRepositoryImpl: DobbyConfigsRepository {
    static let shared = DobbyConfigsRepositoryImpl()
    
    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier)!
    
    private let cloakConfigKey = "cloakConfigKey"
    private let isCloakEnabledKey = "isCloakEnabledKey"
    private let cloakLocalPortKey = "cloakLocalPortKey"
    private let MethodPasswordOutlineKey = "MethodPasswordOutlineKey"
    private let ServerPortOutlineKey = "ServerPortOutlineKey"
    private let isOutlineEnabledKey = "isOutlineEnabledKey"
    private let connectionURLKey = "connectionURLKey"
    private let connectionConfigKey = "connectionConfigKey"
    private let PrefixOutlineKey = "PrefixOutlineKey"
    private let TcpPathOutlineKey = "TcpPathOutlineKey"
    private let isWebsocketEnabledKey = "isWebsocketEnabledKey"
    private let UdpPathOutlineKey = "UdpPathOutlineKey"
    private let isUserInitStopKey = "isUserInitStopKey"

    public func getConnectionURL() -> String {
        return userDefaults.string(forKey: connectionURLKey) ?? ""
    }

    public func setConnectionURL(connectionURL: String) {
        userDefaults.set(connectionURL, forKey: connectionURLKey)
        sync()
    }

    public func getConnectionConfig() -> String {
        return userDefaults.string(forKey: connectionConfigKey) ?? ""
    }

    public func setConnectionConfig(connectionConfig: String) {
        userDefaults.set(connectionConfig, forKey: connectionConfigKey)
        sync()
    }
    
    public func getCloakConfig() -> String {
        return userDefaults.string(forKey: cloakConfigKey) ?? ""
    }
    
    public func setCloakConfig(newConfig: String) {
        userDefaults.set(newConfig, forKey: cloakConfigKey)
        sync()
    }
    
    public func getIsCloakEnabled() -> Bool {
        return userDefaults.bool(forKey: isCloakEnabledKey)
    }

    public func setIsCloakEnabled(isCloakEnabled: Bool) {
        userDefaults.set(isCloakEnabled, forKey: isCloakEnabledKey)
        sync()
    }

    public func getCloakLocalPort() -> Int32 {
        let v = userDefaults.object(forKey: cloakLocalPortKey) as? Int ?? 1984
        return Int32(v)
    }

    public func setCloakLocalPort(port: Int32) {
        userDefaults.set(Int(port), forKey: cloakLocalPortKey)
        sync()
    }

    public func getServerPortOutline() -> String {
        return userDefaults.string(forKey: ServerPortOutlineKey) ?? ""
    }

    public func setServerPortOutline(newConfig: String) {
        userDefaults.set(newConfig, forKey: ServerPortOutlineKey)
        sync()
    }
    
    public func getMethodPasswordOutline() -> String {
        return userDefaults.string(forKey: MethodPasswordOutlineKey) ?? ""
    }
    
    public func setMethodPasswordOutline(newConfig: String) {
        userDefaults.set(newConfig, forKey: MethodPasswordOutlineKey)
        sync()
    }
    
    public func getIsOutlineEnabled() -> Bool {
        return userDefaults.bool(forKey: isOutlineEnabledKey)
    }

    
    public func setIsOutlineEnabled(isOutlineEnabled: Bool) {
        userDefaults.set(isOutlineEnabled, forKey: isOutlineEnabledKey)
        sync()
    }

    public func getPrefixOutline() -> String {
        return userDefaults.string(forKey: PrefixOutlineKey) ?? ""
    }

    public func setPrefixOutline(prefix: String) {
        userDefaults.set(prefix, forKey: PrefixOutlineKey)
        sync()
    }

    public func getTcpPathOutline() -> String {
        return userDefaults.string(forKey: TcpPathOutlineKey) ?? ""
    }

    public func setTcpPathOutline(tcpPath: String) {
        userDefaults.set(tcpPath, forKey: TcpPathOutlineKey)
        sync()
    }

    public func getIsWebsocketEnabled() -> Bool {
        return userDefaults.bool(forKey: isWebsocketEnabledKey)
    }

    public func setIsWebsocketEnabled(enabled: Bool) {
        userDefaults.set(enabled, forKey: isWebsocketEnabledKey)
        sync()
    }

    public func getUdpPathOutline() -> String {
        return userDefaults.string(forKey: UdpPathOutlineKey) ?? ""
    }

    public func setUdpPathOutline(udpPath: String) {
        userDefaults.set(udpPath, forKey: UdpPathOutlineKey)
        sync()
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
    
    public func setAwgConfig(newConfig: String) {}
    
    public func setIsAmneziaWGEnabled(isAmneziaWGEnabled: Bool) {}
    
    public func setVpnInterface(vpnInterface: VpnInterface) {}

    public func couldStart() -> Bool {
        return true
    }
    
    public func getIsUserInitStop() -> Bool {
        return userDefaults.bool(forKey: isUserInitStopKey)
    }

    
    public func setIsUserInitStop(isUserInitStop: Bool) {
        userDefaults.set(isUserInitStop, forKey: isUserInitStopKey)
        sync()
    }

    public func sync() {
        userDefaults.synchronize()
    }
}
