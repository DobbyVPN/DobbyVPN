package com.dobby.feature.main.domain

fun DobbyConfigsRepositoryOutline.clearOutlineConfig() {
    setIsOutlineEnabled(false)
    setMethodPasswordOutline("")
    setServerPortOutline("")
    setPrefixOutline("")
    setIsWebsocketEnabled(false)
    setTcpPathOutline("")
    setUdpPathOutline("")
}

fun DobbyConfigsRepositoryCloak.clearCloakConfig() {
    setIsCloakEnabled(false)
    setCloakConfig("")
}

fun DobbyConfigsRepositoryAwg.clearAwgConfig() {
    setIsAmneziaWGEnabled(false)
    setAwgConfig("")
}

fun DobbyConfigsRepositoryTrustTunnel.clearTrustTunnelConfig() {
    setIsTrustTunnelEnabled(false)
    setTrustTunnelConfig("")
}

fun DobbyConfigsRepository.clearOutlineAndCloakConfig() {
    clearOutlineConfig()
    clearCloakConfig()
}
