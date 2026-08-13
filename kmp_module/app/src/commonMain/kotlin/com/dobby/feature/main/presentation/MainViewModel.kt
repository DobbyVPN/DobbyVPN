package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.logging.Logger
import com.dobby.feature.logging.domain.maskStr
import com.dobby.feature.main.domain.ConnectionStateRepository
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionEvent
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.ui.MainUiState
import com.dobby.vpn.BuildConfig
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.request.get
import io.ktor.client.request.headers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlin.time.Duration.Companion.milliseconds

private val configHttpClient = HttpClient()

/**
 * A deliberately thin UI adapter around sessionapi/v1. Go owns config parsing, profile
 * selection, probing, failover, and the tunnel lifecycle; this class only acquires bytes,
 * asks for permission, and renders ordered session events.
 */
class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val connectionStateRepository: ConnectionStateRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val sessionController: SessionController,
    private val logger: Logger,
) : ViewModel() {
    private val lifecycleMutex = Mutex()
    private val lifecycle = SessionUiLifecycle()
    private var connectionDetectorJob: Job? = null
    private var startInFlight = false
    private var configured = false
    private var pendingPermissionStart = false

    private val _uiState = MutableStateFlow(MainUiState())
    val uiState: StateFlow<MainUiState> = _uiState

    init {
        viewModelScope.launch {
            _uiState.emit(MainUiState(connectionURL = configsRepository.getConnectionURL()))
        }
        viewModelScope.launch {
            permissionEventsChannel.permissionsGrantedEvents.collect(::startVpn)
        }
    }

    fun onConnectionUrlChanged(connectionUrl: String) {
        _uiState.value = _uiState.value.copy(connectionURL = connectionUrl)
        configsRepository.setConnectionURL(connectionUrl)
    }

    fun onConnectionButtonClicked(connectionUrl: String) {
        _uiState.value = _uiState.value.copy(lastFailureCode = null)
        logger.log("Connection button clicked for ${maskStr(connectionUrl)}")
        viewModelScope.launch {
            when (connectionStateRepository.statusFlow.value) {
                VpnConnectionState.DISCONNECTED -> connect(connectionUrl)
                VpnConnectionState.CONNECTING, VpnConnectionState.CONNECTED -> stopVpnService()
                VpnConnectionState.STOPPING -> logger.log("Ignoring connection button while session stop is pending")
            }
        }
    }

    /** Acquires opaque configuration bytes and passes them unchanged to the session API. */
    suspend fun setConfig(connectionUrl: String): Boolean {
        logger.log("Acquiring connection configuration for ${maskStr(connectionUrl)}")
        val rawConfig = runCatching { getConfigBytes(connectionUrl) }
            .onFailure { logger.log("Configuration acquisition failed: type=${it::class.simpleName ?: "UNKNOWN"}") }
            .getOrElse {
                publishFailure(SessionFailureCode.INTERNAL)
                return false
            }

        // Retain only the user-entered source and configuration acquisition record for migration.
        // The exact byte array above, not this decoded record, is what is configured in Go.
        configsRepository.setConnectionURL(connectionUrl)
        configsRepository.setConnectionConfig(rawConfig.decodeToString())

        return when (val result = sessionController.configure(rawConfig)) {
            is SessionControllerResult.Success -> {
                configured = true
                logger.log("Session configuration accepted: profiles=${result.value.profiles.size}")
                true
            }
            is SessionControllerResult.Failure -> {
                configured = false
                logger.log("Session configuration rejected: failureCode=${result.code.name}")
                publishFailure(result.code)
                false
            }
        }
    }

    /** Deprecated detector shim: it now polls session events rather than a health-check state. */
    fun startConnectionStateDetector() {
        if (connectionDetectorJob?.isActive == true) return
        connectionDetectorJob = viewModelScope.launch {
            while (isActive) {
                val afterSequence = lifecycleMutex.withLock { lifecycle.lastSequence }
                when (val result = sessionController.observe(afterSequence)) {
                    is SessionControllerResult.Success -> {
                        for (event in result.value.events) {
                            renderEvent(event)
                        }
                    }
                    is SessionControllerResult.Failure -> {
                        logger.log("Session event poll failed: failureCode=${result.code.name}")
                        publishFailure(result.code)
                    }
                }
                delay(250.milliseconds)
            }
        }
    }

    /** Deprecated detector shim retained for screen and test compatibility. */
    fun stopConnectionStateDetector() {
        connectionDetectorJob?.cancel()
        connectionDetectorJob = null
    }

    /** Starts only AUTO_SELECT; profile choice and failover remain in the Go session. */
    suspend fun startVpnService(): Boolean = lifecycleMutex.withLock {
        if (!configured || startInFlight || lifecycle.activeGeneration != null) {
            logger.log("Ignoring duplicate or unconfigured session start")
            return@withLock false
        }
        startInFlight = true
        when (val result = sessionController.start(SessionStartTarget.AutoSelect)) {
            is SessionControllerResult.Success -> {
                startInFlight = false
                val state = lifecycle.begin(result.value)
                if (state == null) return@withLock false
                publish(state)
                logger.log("Session start accepted for generation=${result.value}")
                startConnectionStateDetector()
                true
            }
            is SessionControllerResult.Failure -> {
                startInFlight = false
                lifecycle.failStart()
                logger.log("Session start rejected: failureCode=${result.code.name}")
                publish(VpnConnectionState.DISCONNECTED, result.code)
                false
            }
        }
    }

    /** Sends a generation-correlated stop to the session API. */
    fun stopVpnService() {
        viewModelScope.launch {
            val generation = lifecycleMutex.withLock {
                val activeGeneration = lifecycle.activeGeneration ?: return@withLock null
                val stoppingState = lifecycle.requestStop()
                if (stoppingState != null) {
                    publish(stoppingState)
                }
                activeGeneration
            } ?: return@launch

            when (val result = sessionController.stop(generation)) {
                is SessionControllerResult.Success -> logger.log("Session stop accepted for generation=$generation")
                is SessionControllerResult.Failure ->
                    logger.log("Session stop rejected: generation=$generation failureCode=${result.code.name}")
                        .also { publishFailure(result.code) }
            }
        }
    }

    suspend fun destroySession() {
        stopConnectionStateDetector()
        when (val result = sessionController.destroy()) {
            is SessionControllerResult.Success -> publish(VpnConnectionState.DISCONNECTED)
            is SessionControllerResult.Failure ->
                logger.log("Session destroy failed: failureCode=${result.code.name}")
                    .also { publishFailure(result.code) }
        }
    }

    private suspend fun connect(connectionUrl: String) {
        if (!setConfig(connectionUrl)) {
            return
        }
        pendingPermissionStart = true
        if (isPermissionCheckNeeded) {
            permissionEventsChannel.checkPermissions()
        } else {
            startVpn(true)
        }
    }

    private suspend fun startVpn(isPermissionGranted: Boolean) {
        if (!pendingPermissionStart) return
        pendingPermissionStart = false
        if (isPermissionGranted) {
            startVpnService()
        } else {
            logger.log("VPN permission was denied; session start was not issued")
            publish(VpnConnectionState.DISCONNECTED)
        }
    }

    private suspend fun renderEvent(event: SessionEvent) {
        lifecycleMutex.withLock {
            val state = lifecycle.render(event)
            if (state != null) {
                publish(
                    state,
                    if (event.state == SessionState.FAILED) {
                        event.failureCode ?: SessionFailureCode.UNKNOWN
                    } else {
                        null
                    },
                )
            }
        }
    }

    private suspend fun publish(
        state: VpnConnectionState,
        failureCode: SessionFailureCode? = null,
    ) {
        _uiState.emit(
            _uiState.value.copy(
                connectionState = state,
                lastFailureCode = failureCode,
            ),
        )
        connectionStateRepository.updateStatus(state)
    }

    private fun publishFailure(code: SessionFailureCode) {
        _uiState.value = _uiState.value.copy(lastFailureCode = code)
    }

    private suspend fun getConfigBytes(connectionUrl: String): ByteArray =
        if (connectionUrl.startsWith("http://") || connectionUrl.startsWith("https://")) {
            configHttpClient.get(connectionUrl) {
                headers { append("User-Agent", "DobbyVPN v${BuildConfig.VERSION_NAME}") }
            }.body()
        } else {
            connectionUrl.encodeToByteArray()
        }
}
