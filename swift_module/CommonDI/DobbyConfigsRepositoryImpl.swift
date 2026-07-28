import app
import Foundation

public class DobbyConfigsRepositoryImpl: DobbyConfigsRepository {
    static let shared = DobbyConfigsRepositoryImpl()

    private var userDefaults: UserDefaults = UserDefaults(suiteName: appGroupIdentifier) ?? UserDefaults.standard
    private let secrets = SharedKeychainSecretStore.shared

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
    private let healthCheckGenerationKey = "healthCheckGenerationKey"
    private let isTrustTunnelEnabledKey = "isTrustTunnelEnabledKey"
    private let trustTunnelConfigKey = "trustTunnelConfigKey"

    private init() {
        secrets.migrate(keys: Self.sensitiveKeys, from: userDefaults)
    }

    private func secret(_ key: String) -> String {
        secrets.string(for: key) ?? ""
    }

    private func setSecret(_ value: String, for key: String) {
        precondition(secrets.set(value, for: key), "secure configuration write failed")
        userDefaults.removeObject(forKey: key)
    }

    public func getConnectionURL() -> String {
        return secret(connectionURLKey)
    }

    public func setConnectionURL(connectionURL: String) {
        setSecret(connectionURL, for: connectionURLKey)

    }

    public func getConnectionConfig() -> String {
        return secret(connectionConfigKey)
    }

    public func setConnectionConfig(connectionConfig: String) {
        setSecret(connectionConfig, for: connectionConfigKey)

    }

    public func getConnectionProfiles() -> String {
        return secret(connectionProfilesKey)
    }

    public func setConnectionProfiles(connectionProfiles: String) {
        setSecret(connectionProfiles, for: connectionProfilesKey)
    }

    public func getActiveConnectionProfileIndex() -> Int32 {
        return Int32(userDefaults.integer(forKey: activeConnectionProfileIndexKey))
    }

    public func setActiveConnectionProfileIndex(index: Int32) {
        userDefaults.set(Int(index), forKey: activeConnectionProfileIndexKey)
    }

    public func getCloakConfig() -> String {
        return secret(cloakConfigKey)
    }

    public func setCloakConfig(newConfig: String) {
        setSecret(newConfig, for: cloakConfigKey)

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
        return secret(serverPortOutlineKey)
    }

    public func setServerPort(newConfig: String) {
        setSecret(newConfig, for: serverPortOutlineKey)

    }

    public func getMethodPasswordOutline() -> String {
        return secret(methodPasswordOutlineKey)
    }

    public func setMethodPasswordOutline(newConfig: String) {
        setSecret(newConfig, for: methodPasswordOutlineKey)

    }

    public func getIsOutlineEnabled() -> Bool {
        return userDefaults.bool(forKey: isOutlineEnabledKey)
    }

    public func setIsOutlineEnabled(isOutlineEnabled: Bool) {
        userDefaults.set(isOutlineEnabled, forKey: isOutlineEnabledKey)

    }

    public func getPrefixOutline() -> String {
        return secret(prefixOutlineKey)
    }

    public func setPrefixOutline(prefix: String) {
        setSecret(prefix, for: prefixOutlineKey)

    }

    public func getTcpPathOutline() -> String {
        return secret(tcpPathOutlineKey)
    }

    public func setTcpPathOutline(tcpPath: String) {
        setSecret(tcpPath, for: tcpPathOutlineKey)

    }

    public func getIsWebsocketEnabled() -> Bool {
        return userDefaults.bool(forKey: isWebsocketEnabledKey)
    }

    public func setIsWebsocketEnabled(enabled: Bool) {
        userDefaults.set(enabled, forKey: isWebsocketEnabledKey)

    }

    public func getUdpPathOutline() -> String {
        return secret(udpPathOutlineKey)
    }

    public func setUdpPathOutline(udpPath: String) {
        setSecret(udpPath, for: udpPathOutlineKey)

    }

    public func getVpnInterface() -> VpnInterface {
        switch userDefaults.string(forKey: vpnInterfaceKey) ?? "CLOAK_OUTLINE" {
        case "XRAY":
            return VpnInterface.xray
        case "TRUST_TUNNEL":
            return VpnInterface.trustTunnel
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
        } else if vpnInterface == VpnInterface.trustTunnel {
            value = "TRUST_TUNNEL"
        } else if vpnInterface == VpnInterface.none {
            value = "NONE"
        } else {
            value = "CLOAK_OUTLINE"
        }
        userDefaults.set(value, forKey: vpnInterfaceKey)
    }

    public func getXrayConfig() -> String {
        return secret(xrayConfigKey)
    }

    public func setXrayConfig(config: String) {
        setSecret(config, for: xrayConfigKey)

    }

    public func getIsXrayEnabled() -> Bool {
        return userDefaults.bool(forKey: isXrayEnabledKey)
    }

    public func setIsXrayEnabled(isXrayEnabled: Bool) {
        setVpnInterface(vpnInterface: isXrayEnabled ? VpnInterface.xray : VpnInterface.cloakOutline)
        userDefaults.set(isXrayEnabled, forKey: isXrayEnabledKey)

    }

    public func getTrustTunnelConfig() -> String {
        return secret(trustTunnelConfigKey)
    }

    public func setTrustTunnelConfig(config: String) {
        setSecret(config, for: trustTunnelConfigKey)
    }

    public func getIsTrustTunnelEnabled() -> Bool {
        return userDefaults.bool(forKey: isTrustTunnelEnabledKey)
    }

    public func setIsTrustTunnelEnabled(isTrustTunnelEnabled: Bool) {
        setVpnInterface(vpnInterface: isTrustTunnelEnabled ? VpnInterface.trustTunnel : VpnInterface.cloakOutline)
        userDefaults.set(isTrustTunnelEnabled, forKey: isTrustTunnelEnabledKey)
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
        return secret(telemetryEndpointKey)
    }

    public func setTelemetryEndpoint(endpoint: String) {
        setSecret(endpoint, for: telemetryEndpointKey)
    }

    public func getTelemetryApiToken() -> String {
        return secret(telemetryApiTokenKey)
    }

    public func setTelemetryApiToken(token: String) {
        setSecret(token, for: telemetryApiTokenKey)
    }

    public func getTelemetryAttributes() -> String {
        return secret(telemetryAttributesKey)
    }

    public func setTelemetryAttributes(config: String) {
        setSecret(config, for: telemetryAttributesKey)
    }

    public func getGeoRoutingConf() -> String {
        return secret(geoRoutingConfKey)
    }

    public func setGeoRoutingConf(geoRoutingConf: String) {
        setSecret(geoRoutingConf, for: geoRoutingConfKey)
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

    /// Diagnostic correlation only.  Consumers must use NetworkExtension status for connection.
    public func setHealthCheckGeneration(_ generation: UInt64) {
        userDefaults.set(String(generation), forKey: healthCheckGenerationKey)
    }

    public func getHealthCheckStateUpdatedAt() -> Double {
        return userDefaults.double(forKey: healthCheckStateUpdatedAtKey)
    }

    private static let sensitiveKeys = [
        "connectionURLKey",
        "connectionConfigKey",
        "connectionProfilesKey",
        "cloakConfigKey",
        "ServerPortOutlineKey",
        "MethodPasswordOutlineKey",
        "PrefixOutlineKey",
        "TcpPathOutlineKey",
        "UdpPathOutlineKey",
        "xrayConfigKey",
        "trustTunnelConfigKey",
        "telemetryEndpointKey",
        "telemetryApiTokenKey",
        "telemetryAttributesKey",
        "geoRoutingConfKey",
        "sessionapi.v1.rawConfiguration",
    ]
}
