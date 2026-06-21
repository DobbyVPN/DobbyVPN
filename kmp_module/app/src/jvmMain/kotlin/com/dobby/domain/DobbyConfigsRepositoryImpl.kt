package com.dobby.domain

import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import interop.healthcheck.HealthCheckLibrary
import java.nio.charset.StandardCharsets
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths
import java.util.prefs.Preferences

internal class DobbyConfigsRepositoryImpl(
    private val prefs: Preferences = Preferences.userRoot(),
    private val healthCheckLibrary: HealthCheckLibrary,
) : DobbyConfigsRepository {
    private val storageDir: Path = Paths.get(
        System.getProperty("user.home") ?: ".",
        ".myapp",
        "configs"
    )
    private val connectionUrlFile = storageDir.resolve("connection-url.txt")
    private val connectionConfigFile = storageDir.resolve("connection-config.toml")
    private val geoRoutingConfFile = storageDir.resolve("geo-routing-conf.txt")

    private fun readLargeString(key: String, file: Path): String {
        return if (Files.exists(file)) {
            Files.readString(file, StandardCharsets.UTF_8)
        } else {
            prefs.get(key, "")
        }
    }

    private fun writeLargeString(key: String, file: Path, value: String) {
        Files.createDirectories(storageDir)
        if (value.isEmpty()) {
            Files.deleteIfExists(file)
        } else {
            Files.writeString(file, value, StandardCharsets.UTF_8)
        }
        prefs.remove(key)
    }

    override fun getVpnInterface(): VpnInterface {
        val prefsResult = prefs.get("vpnInterface", VpnInterface.DEFAULT_VALUE.toString())
            ?: VpnInterface.DEFAULT_VALUE.toString()

        return VpnInterface.valueOf(prefsResult)
    }

    override fun setVpnInterface(vpnInterface: VpnInterface) {
        prefs.put("vpnInterface", vpnInterface.toString())
    }

    override fun getConnectionURL(): String {
        return readLargeString("connectionURL", connectionUrlFile)
    }

    override fun setConnectionURL(connectionURL: String) {
        writeLargeString("connectionURL", connectionUrlFile, connectionURL)
    }

    override fun getConnectionConfig(): String {
        return readLargeString("connectionConfig", connectionConfigFile)
    }

    override fun setConnectionConfig(connectionConfig: String) {
        writeLargeString("connectionConfig", connectionConfigFile, connectionConfig)
    }

    override fun getTelemetryEndpoint(): String {
        return prefs.get("telemetryEndpoint", "")
    }

    override fun setTelemetryEndpoint(endpoint: String) {
        prefs.put("telemetryEndpoint", endpoint)
    }

    override fun getTelemetryApiToken(): String {
        return prefs.get("telemetryApiToken", "")
    }

    override fun setTelemetryApiToken(token: String) {
        prefs.put("telemetryApiToken", token)
    }

    override fun getTelemetryAttributes(): String {
        return prefs.get("telemetryAttributes", "")
    }

    override fun setTelemetryAttributes(config: String) {
        prefs.put("telemetryAttributes", config)
    }

    override fun getCloakConfig(): String {
        return prefs.get("cloakConfig", "")
    }

    override fun setCloakConfig(newConfig: String) {
        prefs.put("cloakConfig", newConfig)
    }

    override fun getIsCloakEnabled(): Boolean {
        return prefs.get("isCloakEnabled", "false").equals("true")
    }

    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        prefs.put("isCloakEnabled", isCloakEnabled.toString())
    }

    override fun getCloakLocalPort(): Int {
        return prefs.get("cloakLocalPort", "1984").toIntOrNull() ?: 1984
    }

    override fun setCloakLocalPort(port: Int) {
        prefs.put("cloakLocalPort", port.toString())
    }

    override fun setServerPort(newConfig: String) {
        prefs.put("ServerPortKey", newConfig)
    }

    override fun setMethodPasswordOutline(newConfig: String) {
        prefs.put("MethodPasswordOutlineKey", newConfig)
    }

    override fun getServerPort(): String {
        return prefs.get("ServerPortKey", "")
    }

    override fun getMethodPasswordOutline(): String {
        return prefs.get("MethodPasswordOutlineKey", "")
    }

    override fun getIsOutlineEnabled(): Boolean {
        return prefs.get("isOutlineEnabled", "false").equals("true")
    }

    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        prefs.put("isOutlineEnabled", isOutlineEnabled.toString())
    }

    override fun getPrefixOutline(): String {
        return prefs.get("PrefixOutlineKey", "")
    }

    override fun setPrefixOutline(prefix: String) {
        prefs.put("PrefixOutlineKey", prefix)
    }

    override fun getTcpPathOutline(): String {
        return prefs.get("TcpPathOutlineKey", "")
    }

    override fun setTcpPathOutline(tcpPath: String) {
        prefs.put("TcpPathOutlineKey", tcpPath)
    }

    override fun getIsWebsocketEnabled(): Boolean {
        return prefs.get("isWebsocketEnabled", "false").equals("true")
    }

    override fun setIsWebsocketEnabled(enabled: Boolean) {
        prefs.put("isWebsocketEnabled", enabled.toString())
    }

    override fun getUdpPathOutline(): String {
        return prefs.get("UdpPathOutlineKey", "")
    }

    override fun setUdpPathOutline(udpPath: String) {
        prefs.put("UdpPathOutlineKey", udpPath)
    }

    override fun getAwgConfig(): String {
        return prefs.get("awgConfig", DEFAULT_AWG_CONFIG)
    }

    override fun setAwgConfig(newConfig: String) {
        prefs.put("awgConfig", newConfig)
    }

    override fun getAwgTomlConfig(): String {
        return prefs.get("awgTomlConfig", "")
    }

    override fun setAwgTomlConfig(newConfig: String) {
        prefs.put("awgTomlConfig", newConfig)
    }

    override fun getIsAmneziaWGEnabled(): Boolean {
        return prefs.get("isAmneziaWGEnabled", "false").equals("true")
    }

    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) {
        prefs.put("isAmneziaWGEnabled", isAmneziaWGEnabled.toString())
    }

    override fun getXrayConfig(): String {
        return prefs.get("xrayConfig", "")
    }

    override fun setXrayConfig(newConfig: String) {
        prefs.put("xrayConfig", newConfig)
    }

    override fun getIsXrayEnabled(): Boolean {
        return prefs.get("isXrayEnabled", "false").equals("true")
    }

    override fun setIsXrayEnabled(isXrayEnabled: Boolean) {
        if (isXrayEnabled) {
            setVpnInterface(VpnInterface.XRAY) // TODO (find other place for this command?)
        } else {
            setVpnInterface(VpnInterface.DEFAULT_VALUE)
        }
        prefs.put("isXrayEnabled", isXrayEnabled.toString())
    }

    override fun couldStart(): Boolean {
        return healthCheckLibrary.CouldStart()
    }

    override fun getGeoRoutingConf(): String {
        return readLargeString("geoRoutingConf", geoRoutingConfFile)
    }

    override fun setGeoRoutingConf(geoRoutingConf: String) {
        writeLargeString("geoRoutingConf", geoRoutingConfFile, geoRoutingConf)
    }

    companion object {
        const val DEFAULT_AWG_CONFIG = """[Interface]
PrivateKey = <...>
Address = <...>
DNS = 8.8.8.8
Jc = 0
Jmin = 0
Jmax = 0
S1 = 0
S2 = 0
H1 = 1
H2 = 2
H3 = 3
H4 = 4

[Peer]
PublicKey = <...>
Endpoint = <...>
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 60
"""
    }
}
