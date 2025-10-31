package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModel
import com.dobby.feature.main.domain.AuthenticationManager
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface

class AuthenticationViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val authenticationManager: AuthenticationManager
): ViewModel() {
    var askedForAuthentication: Boolean = false

    var authenticationSuccess: Boolean = false

    fun authenticate(
        onAuthSuccess: () -> Unit
    ) {
        if (askedForAuthentication) {
            return
        }
        askedForAuthentication = true
        authenticationManager.authenticate {
            authenticationSuccess = true
            onAuthSuccess()
        }
    }

    fun getConfigs(): DobbyConfigsRepository =
        // TODO: check if we're in the red zone
        if (authenticationSuccess) {
            configsRepository
        } else {
            EmptyConfigsRepository()
        }
}

class EmptyConfigsRepository(): DobbyConfigsRepository {
    private var vpnInterface: VpnInterface = VpnInterface.Companion.DEFAULT_VALUE

    override fun getVpnInterface(): VpnInterface
            = vpnInterface

    override fun setVpnInterface(vpnInterface: VpnInterface) {
        this.vpnInterface = vpnInterface
    }

    private var connectionUrl: String = ""

    override fun getConnectionURL(): String = connectionUrl

    override fun setConnectionURL(connectionURL: String) {
        this.connectionUrl = connectionURL
    }

    private var connectionConfig: String = ""

    override fun getConnectionConfig(): String = connectionConfig

    override fun setConnectionConfig(connectionConfig: String) {
        this.connectionConfig = connectionConfig
    }

    private var cloakConfig: String = ""

    override fun getCloakConfig(): String = cloakConfig

    override fun setCloakConfig(newConfig: String) {
        this.cloakConfig = newConfig
    }
    private var isCloakEnabled: Boolean = false

    override fun getIsCloakEnabled(): Boolean = isCloakEnabled

    override fun setIsCloakEnabled(isCloakEnabled: Boolean) {
        this.isCloakEnabled = isCloakEnabled
    }

    private var serverPortOutline: String = ""

    override fun getServerPortOutline(): String = serverPortOutline

    override fun setServerPortOutline(newConfig: String) {
        this.serverPortOutline = newConfig
    }

    private var methodPasswordOutline: String = ""

    override fun getMethodPasswordOutline(): String = methodPasswordOutline

    override fun setMethodPasswordOutline(newConfig: String) {
        this.methodPasswordOutline = newConfig
    }

    private var isOutlineEnabled: Boolean = false

    override fun getIsOutlineEnabled(): Boolean = isOutlineEnabled

    override fun setIsOutlineEnabled(isOutlineEnabled: Boolean) {
        this.isOutlineEnabled = isOutlineEnabled
    }

    private var awgConfig: String = """[Interface]
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

    override fun getAwgConfig(): String = awgConfig

    override fun setAwgConfig(newConfig: String) {
        this.awgConfig = newConfig
    }

    private var isAmneziaWGEnabled: Boolean = false

    override fun getIsAmneziaWGEnabled(): Boolean = isAmneziaWGEnabled

    override fun setIsAmneziaWGEnabled(isAmneziaWGEnabled: Boolean) {
        this.isAmneziaWGEnabled = isAmneziaWGEnabled
    }
}