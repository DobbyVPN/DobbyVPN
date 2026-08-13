package com.dobby.feature.main.ui

import com.dobby.feature.diagnostic.domain.VpnConnectionState

/** Stable, non-secret accessibility values for local and independent UI automation. */
object AutomationSemantics {
    const val CONNECTION_SCREEN = "dobby.connection.screen"
    const val CONNECTION_STATUS = "dobby.connection.status"
    const val SUBSCRIPTION_INPUT = "dobby.subscription.input"
    const val CONNECTION_ACTION = "dobby.connection.action"
    const val FAILURE_STATUS = "dobby.connection.failure"
    const val LOGS = "dobby.logs"
    const val LOG_STORAGE_STATUS = "dobby.logs.storage-status"
    const val SETTINGS_SCREEN = "dobby.settings.screen"
    const val BUILD_VERSION = "dobby.build.version"
    const val BUILD_COMMIT = "dobby.build.commit"

    fun connectionState(state: VpnConnectionState): String = when (state) {
        VpnConnectionState.DISCONNECTED -> "disconnected"
        VpnConnectionState.CONNECTING -> "connecting"
        VpnConnectionState.STOPPING -> "stopping"
        VpnConnectionState.CONNECTED -> "connected"
    }

    fun connectionAction(state: VpnConnectionState): String = when (state) {
        VpnConnectionState.DISCONNECTED -> "start"
        VpnConnectionState.CONNECTING, VpnConnectionState.CONNECTED -> "stop"
        VpnConnectionState.STOPPING -> "stopping"
    }
}
