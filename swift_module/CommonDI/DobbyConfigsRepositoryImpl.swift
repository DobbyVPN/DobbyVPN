import app
import Foundation

public class DobbyConfigsRepositoryImpl: DobbyConfigsRepository {
    static let shared = DobbyConfigsRepositoryImpl()

    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier) ?? UserDefaults.standard

    private let cloakConfigKey = "cloakConfigKey"
    private let isCloakEnabledKey = "isCloakEnabledKey"
    private let cloakLocalPortKey = "cloakLocalPortKey"
    private let methodPasswordOutlineKey = "MethodPasswordOutlineKey"
    private let serverPortOutlineKey = "ServerPortOutlineKey"
    private let isOutlineEnabledKey = "isOutlineEnabledKey"
    private let connectionURLKey = "connectionURLKey"
    private let connectionConfigKey = "connectionConfigKey"
    private let connectionProfilesKey = "connectionProfilesKey"
    private let activeConnectionProfileIndexKey = "activeConnectionProfileIndexKey"
    private let prefixOutlineKey = "PrefixOutlineKey"
    private let tcpPathOutlineKey = "TcpPathOutlineKey"
    private let isWebsocketEnabledKey = "isWebsocketEnabledKey"
    private let udpPathOutlineKey = "UdpPathOutlineKey"
    private let isUserInitStopKey = "isUserInitStopKey"
    private let geoRoutingConfKey = "geoRoutingConfKey"
    private let vpnInterfaceKey = "vpnInterfaceKey"
    private let isXrayEnabledKey = "isXrayEnabledKey"
    private let xrayConfigKey = "xrayConfigKey"
    private let telemetryEndpointKey = "telemetryEndpointKey"
    private let telemetryApiTokenKey = "telemetryApiTokenKey"
    private let telemetryAttributesKey = "telemetryAttributesKey"
    private let healthCheckStateKey = "healthCheckStateKey"
    private let healthCheckStateUpdatedAtKey = "healthCheckStateUpdatedAtKey"

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

    public func getConnectionProfiles() -> String {
        return userDefaults.string(forKey: connectionProfilesKey) ?? ""
    }

    public func setConnectionProfiles(connectionProfiles: String) {
        userDefaults.set(connectionProfiles, forKey: connectionProfilesKey)
    }

    public func getActiveConnectionProfileIndex() -> Int32 {
        return Int32(userDefaults.integer(forKey: activeConnectionProfileIndexKey))
    }

    public func setActiveConnectionProfileIndex(index: Int32) {
        userDefaults.set(Int(index), forKey: activeConnectionProfileIndexKey)
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

    public func getCloakLocalPort() -> Int32 {
        let portValue = userDefaults.object(forKey: cloakLocalPortKey) as? Int ?? 1984
        return Int32(portValue)
    }

    public func setCloakLocalPort(port: Int32) {
        userDefaults.set(Int(port), forKey: cloakLocalPortKey)

    }

    public func getServerPort() -> String {
        return userDefaults.string(forKey: serverPortOutlineKey) ?? ""
    }

    public func setServerPort(newConfig: String) {
        userDefaults.set(newConfig, forKey: serverPortOutlineKey)

    }

    public func getMethodPasswordOutline() -> String {
        return userDefaults.string(forKey: methodPasswordOutlineKey) ?? ""
    }

    public func setMethodPasswordOutline(newConfig: String) {
        userDefaults.set(newConfig, forKey: methodPasswordOutlineKey)

    }

    public func getIsOutlineEnabled() -> Bool {
        return userDefaults.bool(forKey: isOutlineEnabledKey)
    }

    public func setIsOutlineEnabled(isOutlineEnabled: Bool) {
        userDefaults.set(isOutlineEnabled, forKey: isOutlineEnabledKey)

    }

    public func getPrefixOutline() -> String {
        return userDefaults.string(forKey: prefixOutlineKey) ?? ""
    }

    public func setPrefixOutline(prefix: String) {
        userDefaults.set(prefix, forKey: prefixOutlineKey)

    }

    public func getTcpPathOutline() -> String {
        return userDefaults.string(forKey: tcpPathOutlineKey) ?? ""
    }

    public func setTcpPathOutline(tcpPath: String) {
        userDefaults.set(tcpPath, forKey: tcpPathOutlineKey)

    }

    public func getIsWebsocketEnabled() -> Bool {
        return userDefaults.bool(forKey: isWebsocketEnabledKey)
    }

    public func setIsWebsocketEnabled(enabled: Bool) {
        userDefaults.set(enabled, forKey: isWebsocketEnabledKey)

    }

    public func getUdpPathOutline() -> String {
        return userDefaults.string(forKey: udpPathOutlineKey) ?? ""
    }

    public func setUdpPathOutline(udpPath: String) {
        userDefaults.set(udpPath, forKey: udpPathOutlineKey)

    }

    public func getVpnInterface() -> VpnInterface {
        switch userDefaults.string(forKey: vpnInterfaceKey) ?? "CLOAK_OUTLINE" {
        case "XRAY":
            return VpnInterface.xray
        case "NONE":
            return VpnInterface.none
        default:
            return VpnInterface.cloakOutline
        }
    }

    public func setVpnInterface(vpnInterface: VpnInterface) {
        let value: String
        if vpnInterface == VpnInterface.xray {
            value = "XRAY"
        } else if vpnInterface == VpnInterface.none {
            value = "NONE"
        } else {
            value = "CLOAK_OUTLINE"
        }
        userDefaults.set(value, forKey: vpnInterfaceKey)
    }

    public func getXrayConfig() -> String {
        return userDefaults.string(forKey: xrayConfigKey) ?? ""
    }

    public func setXrayConfig(config: String) {
        userDefaults.set(config, forKey: xrayConfigKey)

    }

    public func getIsXrayEnabled() -> Bool {
        return userDefaults.bool(forKey: isXrayEnabledKey)
    }

    public func setIsXrayEnabled(isXrayEnabled: Bool) {
        setVpnInterface(vpnInterface: isXrayEnabled ? VpnInterface.xray : VpnInterface.cloakOutline)
        userDefaults.set(isXrayEnabled, forKey: isXrayEnabledKey)

    }

    public func couldStart() -> Bool {
        return true
    }

    public func getIsUserInitStop() -> Bool {
        return userDefaults.bool(forKey: isUserInitStopKey)
    }

    public func setIsUserInitStop(isUserInitStop: Bool) {
        userDefaults.set(isUserInitStop, forKey: isUserInitStopKey)
    }

    public func getTelemetryEndpoint() -> String {
        return userDefaults.string(forKey: telemetryEndpointKey) ?? ""
    }

    public func setTelemetryEndpoint(endpoint: String) {
        userDefaults.set(endpoint, forKey: telemetryEndpointKey)
    }

    public func getTelemetryApiToken() -> String {
        return userDefaults.string(forKey: telemetryApiTokenKey) ?? ""
    }

    public func setTelemetryApiToken(token: String) {
        userDefaults.set(token, forKey: telemetryApiTokenKey)
    }

    public func getTelemetryAttributes() -> String {
        return userDefaults.string(forKey: telemetryAttributesKey) ?? ""
    }

    public func setTelemetryAttributes(config: String) {
        userDefaults.set(config, forKey: telemetryAttributesKey)
    }

    public func getGeoRoutingConf() -> String {
        return userDefaults.string(forKey: geoRoutingConfKey) ?? ""
    }

    public func setGeoRoutingConf(geoRoutingConf: String) {
        userDefaults.set(geoRoutingConf, forKey: geoRoutingConfKey)
    }

    public func getHealthCheckState() -> Int32 {
        if userDefaults.object(forKey: healthCheckStateKey) == nil {
            return -1
        }
        return Int32(userDefaults.integer(forKey: healthCheckStateKey))
    }

    public func setHealthCheckState(state: Int32) {
        userDefaults.set(Int(state), forKey: healthCheckStateKey)
        userDefaults.set(Date().timeIntervalSince1970, forKey: healthCheckStateUpdatedAtKey)
    }

    public func getHealthCheckStateUpdatedAt() -> Double {
        return userDefaults.double(forKey: healthCheckStateUpdatedAtKey)
    }
}
