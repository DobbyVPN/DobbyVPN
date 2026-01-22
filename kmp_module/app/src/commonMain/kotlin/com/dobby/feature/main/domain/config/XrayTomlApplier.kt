package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.DobbyConfigsRepositoryXray
import com.dobby.feature.main.domain.XrayClientConfig
import com.dobby.feature.main.domain.clearXrayConfig
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.json.putJsonObject

internal class XrayTomlApplier(
    private val xrayRepo: DobbyConfigsRepositoryXray,
    private val logger: Logger,
) {

    fun apply(config: XrayClientConfig): Boolean {
        logger.log("Detected [Xray] config, applying Xray parameters")

        val address = config.address?.trim().orEmpty()
        val port = config.port ?: 443
        val id = config.id?.trim().orEmpty()
        val publicKey = config.publicKey?.trim().orEmpty()

        if (address.isEmpty() || id.isEmpty() || publicKey.isEmpty()) {
            logger.log("Invalid [Xray]: Address, ID, and PublicKey are required. Disabling Xray.")
            xrayRepo.clearXrayConfig()
            return false
        }

        val xrayJson = buildXrayJson(config)

        xrayRepo.setIsXrayEnabled(true)
        xrayRepo.setXrayConfig(xrayJson)

        logger.log("Xray config saved successfully: ${config.protocol}://${maskStr(address)}:${port}")
        return true
    }

    private fun buildXrayJson(config: XrayClientConfig): String {
        val json = Json { prettyPrint = true }

        val root = buildJsonObject {
            putJsonObject("log") {
                put("loglevel", "warning")
            }

            // Inbounds
            putJsonArray("inbounds") {
                add(buildJsonObject {
                    put("tag", "socks-in")
                    put("port", 10808)
                    put("listen", "127.0.0.1")
                    put("protocol", "socks")
                    putJsonObject("settings") {
                        put("udp", true)
                    }
                })
            }

            // Outbounds
            putJsonArray("outbounds") {
                // 1. Proxy Outbound (VLESS)
                add(buildJsonObject {
                    put("tag", config.tag ?: "proxy")
                    put("protocol", config.protocol ?: "vless")

                    putJsonObject("settings") {
                        putJsonArray("vnext") {
                            add(buildJsonObject {
                                put("address", config.address)
                                put("port", config.port ?: 443)
                                putJsonArray("users") {
                                    add(buildJsonObject {
                                        put("id", config.id)
                                        put("flow", config.flow ?: "xtls-rprx-vision")
                                        put("encryption", config.encryption ?: "none")
                                    })
                                }
                            })
                        }
                    }

                    putJsonObject("streamSettings") {
                        put("network", config.network ?: "tcp")
                        put("security", config.security ?: "reality")
                        putJsonObject("realitySettings") {
                            put("show", false)
                            put("fingerprint", config.fingerprint ?: "chrome")
                            put("serverName", config.serverName ?: "www.google.com")
                            put("publicKey", config.publicKey)
                            put("shortId", config.shortId ?: "")
                            put("spiderX", config.spiderX ?: "/")
                        }
                    }
                })

                // 2. Direct Outbound
                add(buildJsonObject {
                    put("tag", "direct")
                    put("protocol", "freedom")
                })
            }
        }

        return json.encodeToString(root)
    }
}