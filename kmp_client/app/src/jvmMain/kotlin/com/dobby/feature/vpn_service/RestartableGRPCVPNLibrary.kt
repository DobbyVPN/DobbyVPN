package com.dobby.feature.vpn_service

import com.dobby.feature.logging.Logger
import interop.GRPCVPNLibrary
import interop.data.TcpPingResponce
import interop.data.UrlTestResponce
import interop.exceptions.VPNServiceConnectionException

class RestartableGRPCVPNLibrary(private val logger: Logger) : GRPCVPNLibrary() {
    override fun GetOutlineLastError(): String {
        try {
            return super.GetOutlineLastError()
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to GetOutlineLastError: ${e.parent}")

            return ""
        }
    }

    override fun StartOutline(key: String): Int {
        try {
            return super.StartOutline(key)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StartOutline: ${e.parent}")

            return -1
        }
    }

    override fun StopOutline() {
        try {
            return super.StopOutline()
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StopOutline: ${e.parent}")
        }
    }

    override fun StartHealthCheck(period: Int, sendMetrics: Boolean) {
        try {
            return super.StartHealthCheck(period, sendMetrics)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StartHealthCheck: ${e.parent}")
        }
    }

    override fun StopHealthCheck() {
        try {
            return super.StopHealthCheck()
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StopHealthCheck: ${e.parent}")
        }
    }

    override fun Status(): String {
        try {
            return super.Status()
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to Status: ${e.parent}")

            return ""
        }
    }

    override fun TcpPing(address: String): TcpPingResponce {
        try {
            return super.TcpPing(address)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to TcpPing: ${e.parent}")

            return TcpPingResponce(0, "")
        }
    }

    override fun UrlTest(url: String, standard: Int): UrlTestResponce {
        try {
            return super.UrlTest(url, standard)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to UrlTest: ${e.parent}")

            return UrlTestResponce(0, "")
        }
    }

    override fun StartCloakClient(localHost: String, localPort: String, config: String, udp: Boolean) {
        try {
            return super.StartCloakClient(localHost, localPort, config, udp)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StartCloakClient: ${e.parent}")
        }
    }

    override fun StopCloakClient() {
        try {
            return super.StopCloakClient()
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StopCloakClient: ${e.parent}")
        }
    }

    override fun StartAwg(key: String, config: String) {
        try {
            return super.StartAwg(key, config)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StartAwg: ${e.parent}")
        }
    }

    override fun StopAwg() {
        try {
            return super.StopAwg()
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to StopAwg: ${e.parent}")
        }
    }

    override fun CouldStart(): Boolean {
        try {
            return super.CouldStart()
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to CouldStart: ${e.parent}")

            return false
        }
    }

    override fun InitLogger(path: String) {
        try {
            return super.InitLogger(path)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to InitLogger: ${e.parent}")
        }
    }

    override fun CheckServerAlive(address: String, port: Int): Int {
        try {
            return super.CheckServerAlive(address, port)
        } catch (e: VPNServiceConnectionException) {
            logger.log("[ERROR] Failed to CheckServerAlive: ${e.parent}")

            return 0
        }
    }
}