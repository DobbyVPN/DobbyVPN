package com.dobby.feature.authentication.domain

import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.VpnInterface
import com.russhwolf.settings.Settings
import dev.jordond.compass.permissions.PermissionState
import kotlinx.coroutines.MainScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import org.koin.core.component.KoinComponent
import org.koin.core.component.get

object HideConfigsManager: KoinComponent {
    private val configsRepository: DobbyConfigsRepository = get()
    private val authenticationManager: AuthenticationManager = get()

    private val scope = MainScope()

    val settings = Settings()

    enum class TryEnableHideConfigsResult {
        SUCCESS, ERROR_NO_BIOMETRICS, ERROR_NO_LOCATION, IN_PROGRESS
    }

    suspend fun tryEnableHideConfigs(): TryEnableHideConfigsResult {
        if (!authenticationManager.isAuthenticationAvailable()) {
            return TryEnableHideConfigsResult.ERROR_NO_BIOMETRICS
        }
        return if (LocationManager.requestLocationPermission() == PermissionState.Granted) {
            settings.putBoolean("isHideConfigsEnabled", true)
            TryEnableHideConfigsResult.SUCCESS
        } else {
            TryEnableHideConfigsResult.ERROR_NO_LOCATION
        }
    }

    fun disableHideConfigs() = settings.putBoolean("isHideConfigsEnabled", false)

    suspend fun isHideConfigsEnabled(): Boolean {
        if (!settings.hasKey("isHideConfigsEnabled")) {
            // when the user opens the app for the first time, try to enable the 'hide configurations' feature
            settings.putBoolean("isHideConfigsEnabled", tryEnableHideConfigs() == TryEnableHideConfigsResult.SUCCESS)
        }
        return settings.getBoolean("isHideConfigsEnabled", false)
    }

    enum class AuthStatus {
        NONE, IN_PROGRESS, SUCCESS, FAILURE
    }
    var authStatus: AuthStatus = AuthStatus.NONE
         set(value) {
            field = value
            scope.launch {
                _authState.emit(field)
            }
        }

    private val _authState: MutableStateFlow<AuthStatus> = MutableStateFlow(authStatus)
    val authState: StateFlow<AuthStatus> = _authState

    fun authenticate(
        onSuccess: (configsRepository: DobbyConfigsRepository) -> Unit,
        onFailure: () -> Unit
    ) {
        if (authStatus != AuthStatus.NONE) {
            return
        }
        authStatus = AuthStatus.IN_PROGRESS
        scope.launch {
            if (!isHideConfigsEnabled()) {
                onSuccess(configsRepository)
                authStatus = AuthStatus.SUCCESS
                return@launch
            }
            // if 'hide configurations' feature is enabled, show the authentication prompt
            authenticationManager.authenticate(
                onAuthSuccess = {
                    onSuccess(configsRepository)
                    authStatus = AuthStatus.SUCCESS
                }, onAuthFailure = {
                    scope.launch {
                        val inRedZone = LocationManager.inRedZone()
                        if (inRedZone != RedZoneCheckResult.RED_ZONE) {
                            onSuccess(configsRepository)
                            authStatus = AuthStatus.SUCCESS
                        } else {
                            onFailure()
                            authStatus = AuthStatus.FAILURE
                        }
                    }
                }
            )
        }

    }

    fun getConfigs(): DobbyConfigsRepository {
        return if (authStatus == AuthStatus.SUCCESS) {
            configsRepository
        } else {
            EmptyConfigsRepository
        }
    }

    object EmptyConfigsRepository: DobbyConfigsRepository {
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
Jmincompanion object EmptyConfigsRepository: DobbyConfigsRepository {
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
 = 0
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
}
