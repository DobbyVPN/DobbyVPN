package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.AmneziaWGConfig
import com.dobby.feature.main.domain.AmneziaWGInterfaceConfig
import com.dobby.feature.main.domain.AmneziaWGPeerConfig
import com.dobby.feature.main.domain.DobbyConfigsRepositoryAwg
import com.dobby.feature.main.domain.DobbyConfigsRepositoryVpn
import com.dobby.feature.main.domain.VpnInterface
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

internal class AmneziaWGTomlApplier(
    val vpnRepo: DobbyConfigsRepositoryVpn,
    private val awgRepo: DobbyConfigsRepositoryAwg,
    private val logger: Logger,
) {
    fun apply(amneziaWGConfig: AmneziaWGConfig) {
        logger.log("Detected [AmneziaWG] config, applying AmneziaWG parameters")

        // TODO: validate amneziaWGConfig

        val tomlConfig = buildAmneziaWGQuickConfig(amneziaWGConfig)
        val maskedConfig = buildMaskedAmneziaWGJson(amneziaWGConfig)

        vpnRepo.setVpnInterface(VpnInterface.AMNEZIA_WG)
        awgRepo.setIsAmneziaWGEnabled(true)
        awgRepo.setAwgConfig(tomlConfig)

        logger.log("AmneziaWG config saved successfully (config=$maskedConfig)")
    }

    private fun buildAmneziaWGQuickConfig(config: AmneziaWGConfig): String {
        val stringBuilder = StringBuilder()
        stringBuilder.append("[Interface]\n")
        stringBuilder.append("PrivateKey = ${config.Interface.PrivateKey}\n")
        stringBuilder.append("# PublicKey = ${config.Interface.PublicKey}\n")
        stringBuilder.append("Address = ${config.Interface.Address}\n")
        config.Interface.DNS?.let { stringBuilder.append("DNS = $it\n") }
        config.Interface.MTU?.let { stringBuilder.append("MTU = $it\n") }
        config.Interface.Jc?.let { stringBuilder.append("Jc = $it\n") }
        config.Interface.Jmin?.let { stringBuilder.append("Jmin = $it\n") }
        config.Interface.Jmax?.let { stringBuilder.append("Jmax = $it\n") }
        config.Interface.S1?.let { stringBuilder.append("S1 = $it\n") }
        config.Interface.S2?.let { stringBuilder.append("S2 = $it\n") }
        config.Interface.S3?.let { stringBuilder.append("S3 = $it\n") }
        config.Interface.S4?.let { stringBuilder.append("S4 = $it\n") }
        config.Interface.H1?.let { stringBuilder.append("H1 = $it\n") }
        config.Interface.H2?.let { stringBuilder.append("H2 = $it\n") }
        config.Interface.H3?.let { stringBuilder.append("H3 = $it\n") }
        config.Interface.H4?.let { stringBuilder.append("H4 = $it\n") }
        config.Interface.I1?.let { stringBuilder.append("I1 = $it\n") }
        config.Interface.I2?.let { stringBuilder.append("I2 = $it\n") }
        config.Interface.I3?.let { stringBuilder.append("I3 = $it\n") }
        config.Interface.I4?.let { stringBuilder.append("I4 = $it\n") }
        config.Interface.I5?.let { stringBuilder.append("I5 = $it\n") }
        stringBuilder.append("\n")

        for (peerConfig in config.Peer) {
            stringBuilder.append("[Peer]\n")
            stringBuilder.append("PublicKey = ${peerConfig.PublicKey}\n")
            peerConfig.PresharedKey?.let { stringBuilder.append("PresharedKey = $it\n") }
            stringBuilder.append("Endpoint = ${peerConfig.Endpoint}\n")
            stringBuilder.append("AllowedIPs = ${peerConfig.AllowedIPs}\n")
            peerConfig.PersistentKeepalive?.let { stringBuilder.append("PersistentKeepalive = $it\n") }
        }

        return stringBuilder.toString()
    }

    private fun buildMaskedAmneziaWGJson(config: AmneziaWGConfig): String {
        val json = Json { prettyPrint = true }
        val maskedConfig = AmneziaWGConfig(
            Interface = maskInterface(config.Interface),
            Peer = config.Peer.map(::maskPeer)
        )

        return json.encodeToString(maskedConfig)
    }

    private fun maskInterface(config: AmneziaWGInterfaceConfig): AmneziaWGInterfaceConfig =
        AmneziaWGInterfaceConfig(
            PrivateKey = maskStr(config.PrivateKey),
            PublicKey = config.PublicKey?.let(::maskStr),
            Address = config.Address,
            DNS = config.DNS,
            MTU = config.MTU,
            Jc = config.Jc,
            Jmin = config.Jmin,
            Jmax = config.Jmax,
            S1 = config.S1,
            S2 = config.S2,
            S3 = config.S3,
            S4 = config.S4,
            H1 = config.H1,
            H2 = config.H2,
            H3 = config.H3,
            H4 = config.H4,
            I1 = config.I1,
            I2 = config.I2,
            I3 = config.I3,
            I4 = config.I4,
            I5 = config.I5,
        )

    private fun maskPeer(config: AmneziaWGPeerConfig): AmneziaWGPeerConfig =
        AmneziaWGPeerConfig(
            PublicKey = maskStr(config.PublicKey),
            PresharedKey = config.PresharedKey?.let(::maskStr),
            Endpoint = config.Endpoint,
            AllowedIPs = config.AllowedIPs,
            PersistentKeepalive = config.PersistentKeepalive,
        )
}
