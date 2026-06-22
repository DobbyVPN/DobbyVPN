package com.dobby.feature.main.domain

fun DobbyConfigsRepositoryOutline.clearOutlineConfig() {
    setIsOutlineEnabled(false)
    setMethodPasswordOutline("")
    setServerPort("")
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
    setAwgTomlConfig("")
}

fun DobbyConfigsRepositoryXray.clearXrayConfig() {
    setIsXrayEnabled(false)
    setXrayConfig("")
}


fun DobbyConfigsRepository.clearVpnConfig() {
    setVpnInterface(VpnInterface.NONE)
    setConnectionProfiles("")
    setActiveConnectionProfileIndex(0)
    clearOutlineConfig()
    clearCloakConfig()
    clearXrayConfig()
    clearAwgConfig()
}
