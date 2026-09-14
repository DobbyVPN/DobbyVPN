package com.dobby.feature.main.presentation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.dobby.feature.diagnostic.domain.VpnConnectionState
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.DobbyConfigsRepository
import com.dobby.feature.main.domain.PermissionEventsChannel
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionSourceKind
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.ui.MainUiState
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext

private const val snapshotRetryDelayMillis = 1_000L

/** Go owns configuration, selection, failover, and lifecycle; this class maps snapshots to UI. */
class MainViewModel(
    private val configsRepository: DobbyConfigsRepository,
    private val permissionEventsChannel: PermissionEventsChannel,
    private val sessionController: SessionController,
    private val logger: Logger,
) : ViewModel() {
    private val lifecycleMutex = Mutex()
    private val revisionGate = SessionRevisionGate()
    // An accepted Start can precede its Watch snapshot; retain its generation for Stop.
    private var activeGeneration: ULong? = null
    // Configure acceptance can precede the snapshot that reports configured=true.
    private var configured = false
    private var pendingPermissionStart = false

    private val _uiState = MutableStateFlow(MainUiState())
    val uiState: StateFlow<MainUiState> = _uiState

    init {
        _uiState.value = MainUiState(connectionURL = configsRepository.getConnectionURL())
        viewModelScope.launch {
            permissionEventsChannel.permissionsGrantedEvents.collect(::startVpn)
        }
        observeSnapshots()
    }

    fun onConnectionUrlChanged(connectionUrl: String) {
        _uiState.value = _uiState.value.copy(connectionURL = connectionUrl)
    }

    fun onConnectionButtonClicked(connectionUrl: String) {
        _uiState.value = _uiState.value.copy(lastFailureCode = null, lastFailureMessage = null)
        logger.log("Connection button clicked")
        viewModelScope.launch {
            when (_uiState.value.connectionState) {
                VpnConnectionState.DISCONNECTED -> connect(connectionUrl)
                VpnConnectionState.CONNECTING, VpnConnectionState.RECONNECTING, VpnConnectionState.CONNECTED ->
                    stopVpnService()
                VpnConnectionState.STOPPING -> logger.log("Ignoring connection button while stop is pending")
            }
        }
    }

    /** Sends the entered source unchanged; only an accepted URL is persisted. */
    suspend fun setConfig(connectionUrl: String): Boolean {
        val result = withContext(Dispatchers.Default) {
            sessionController.configure(connectionUrl.encodeToByteArray())
        }
        return when (result) {
            is SessionControllerResult.Success -> {
                lifecycleMutex.withLock {
                    revisionGate.advance(result.value.sessionId, result.value.sequence)
                    configured = true
                }
                _uiState.value = _uiState.value.copy(
                    lastFailureCode = null,
                    lastFailureMessage = null,
                    activeProfile = null,
                    warnings = result.value.warnings,
                )
                if (result.value.sourceKind == SessionSourceKind.URL) {
                    configsRepository.setConnectionURL(connectionUrl)
                }
                logger.log("Session configuration accepted: profiles=${result.value.profiles.size}")
                true
            }
            is SessionControllerResult.Failure -> {
                logger.error("Session configuration rejected: failureCode=${result.code.name}")
                publishFailure(result.code, result.message)
                false
            }
        }
    }

    /** Reattaches by consuming the current snapshot stream; no event history or cursor is needed. */
    @Suppress("TooGenericExceptionCaught")
    private fun observeSnapshots() {
        viewModelScope.launch {
            while (isActive) {
                try {
                    sessionController.watch()
                        .flowOn(Dispatchers.Default)
                        .collect(::renderSnapshot)
                } catch (cancelled: CancellationException) {
                    throw cancelled
                } catch (_: Exception) {
                    if (isActive) {
                        logger.error("Session snapshot stream failed")
                        publishFailure(SessionFailureCode.INTERNAL, "Session snapshot stream failed")
                    }
                }
                if (isActive) delay(snapshotRetryDelayMillis)
            }
        }
    }

    /** Starts Go's automatic protocol selection after Android permission succeeds. */
    suspend fun startVpnService(): Boolean = lifecycleMutex.withLock {
        if (!configured || activeGeneration != null) {
            logger.log("Ignoring duplicate or unconfigured session start")
            return@withLock false
        }
        when (val result = withContext(Dispatchers.Default) {
            sessionController.start(SessionStartTarget.AutoSelect)
        }) {
            is SessionControllerResult.Success -> {
                revisionGate.advance(result.value.sessionId, result.value.sequence)
                activeGeneration = result.value.generation
                publish(VpnConnectionState.CONNECTING)
                true
            }
            is SessionControllerResult.Failure -> {
                activeGeneration = null
                logger.error("Session start rejected: failureCode=${result.code.name}")
                publish(VpnConnectionState.DISCONNECTED, result.code, result.message)
                false
            }
        }
    }

    fun stopVpnService() {
        viewModelScope.launch {
            val generation = lifecycleMutex.withLock {
                val active = activeGeneration ?: return@withLock null
                publish(VpnConnectionState.STOPPING)
                active
            } ?: return@launch

            when (val result = withContext(Dispatchers.Default) { sessionController.stop(generation) }) {
                is SessionControllerResult.Success -> {
                    lifecycleMutex.withLock {
                        revisionGate.advance(result.value.sessionId, result.value.sequence)
                    }
                    logger.log("Session stop accepted for generation=$generation")
                }
                is SessionControllerResult.Failure -> {
                    logger.error("Session stop rejected: generation=$generation failureCode=${result.code.name}")
                    publishFailure(result.code, result.message)
                }
            }
        }
    }

    private suspend fun connect(connectionUrl: String) {
        if (!setConfig(connectionUrl)) return
        pendingPermissionStart = true
        if (isPermissionCheckNeeded) permissionEventsChannel.checkPermissions() else startVpn(true)
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

    private suspend fun renderSnapshot(snapshot: SessionSnapshot) {
        lifecycleMutex.withLock {
            if (!revisionGate.accept(snapshot.sessionId, snapshot.sequence)) return@withLock
            configured = snapshot.configured
            activeGeneration = snapshot.generation.takeIf {
                snapshot.recovering || snapshot.state in setOf(
                    SessionState.PROBING,
                    SessionState.PREPARING,
                    SessionState.CONNECTED,
                    SessionState.STOPPING,
                )
            }
            val priorUiState = _uiState.value
            val snapshotFailure = snapshot.lastFailure.takeIf { snapshot.state == SessionState.FAILED }
            val successfulConnectionTransition =
                snapshot.state == SessionState.CONNECTED && priorUiState.connectionState != VpnConnectionState.CONNECTED
            val connectedAfterRejectedStop =
                successfulConnectionTransition &&
                    priorUiState.connectionState == VpnConnectionState.STOPPING &&
                    (priorUiState.lastFailureCode != null || priorUiState.lastFailureMessage != null)
            val clearPreviousFailure =
                snapshot.state == SessionState.FAILED ||
                    (successfulConnectionTransition && !connectedAfterRejectedStop)
            _uiState.emit(
                priorUiState.copy(
                    connectionState = if (snapshot.recovering) {
                        VpnConnectionState.RECONNECTING
                    } else {
                        snapshot.state.toConnectionState()
                    },
                    lastFailureCode = snapshotFailure?.code
                        ?: priorUiState.lastFailureCode.takeUnless { clearPreviousFailure },
                    lastFailureMessage = snapshotFailure?.message
                        ?: priorUiState.lastFailureMessage.takeUnless { clearPreviousFailure },
                    activeProfile = snapshot.activeProfile.takeIf { snapshot.state == SessionState.CONNECTED },
                    warnings = snapshot.warnings,
                ),
            )
        }
    }

    private suspend fun publish(
        state: VpnConnectionState,
        failureCode: SessionFailureCode? = null,
        failureMessage: String? = null,
    ) {
        _uiState.emit(
            _uiState.value.copy(
                connectionState = state,
                lastFailureCode = failureCode,
                lastFailureMessage = failureMessage,
                activeProfile = if (state == VpnConnectionState.CONNECTED) _uiState.value.activeProfile else null,
            ),
        )
    }

    private fun publishFailure(code: SessionFailureCode, message: String) {
        _uiState.value = _uiState.value.copy(lastFailureCode = code, lastFailureMessage = message)
    }
}

/** Prevents a delayed watch response from rolling UI state behind an accepted command revision. */
internal class SessionRevisionGate {
    private var sessionId: String? = null
    private var sequence: ULong = 0uL

    fun advance(sessionId: String, sequence: ULong) {
        if (this.sessionId != sessionId) {
            this.sessionId = sessionId
            this.sequence = sequence
        } else if (sequence > this.sequence) {
            this.sequence = sequence
        }
    }

    fun accept(sessionId: String, sequence: ULong): Boolean {
        if (this.sessionId != sessionId) {
            advance(sessionId, sequence)
            return true
        }
        if (sequence < this.sequence) return false
        this.sequence = sequence
        return true
    }
}

private fun SessionState.toConnectionState() = when (this) {
    SessionState.PROBING, SessionState.PREPARING -> VpnConnectionState.CONNECTING
    SessionState.CONNECTED -> VpnConnectionState.CONNECTED
    SessionState.STOPPING -> VpnConnectionState.STOPPING
    SessionState.IDLE, SessionState.CONFIGURED, SessionState.FAILED -> VpnConnectionState.DISCONNECTED
}
