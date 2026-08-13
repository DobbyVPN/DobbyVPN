package com.dobby.feature.main.ui

import com.dobby.feature.diagnostic.domain.VpnConnectionState
import kotlin.test.Test
import kotlin.test.assertEquals

class AutomationSemanticsTest {
    @Test
    fun identifiers_are_stable_non_secret_values() {
        assertEquals("dobby.connection.status", AutomationSemantics.CONNECTION_STATUS)
        assertEquals("dobby.subscription.input", AutomationSemantics.SUBSCRIPTION_INPUT)
        assertEquals("dobby.connection.action", AutomationSemantics.CONNECTION_ACTION)
        assertEquals("dobby.connection.failure", AutomationSemantics.FAILURE_STATUS)
        assertEquals("dobby.build.commit", AutomationSemantics.BUILD_COMMIT)
    }

    @Test
    fun state_and_action_values_cover_every_ui_state() {
        assertEquals("disconnected", AutomationSemantics.connectionState(VpnConnectionState.DISCONNECTED))
        assertEquals("connecting", AutomationSemantics.connectionState(VpnConnectionState.CONNECTING))
        assertEquals("stopping", AutomationSemantics.connectionState(VpnConnectionState.STOPPING))
        assertEquals("connected", AutomationSemantics.connectionState(VpnConnectionState.CONNECTED))
        assertEquals("start", AutomationSemantics.connectionAction(VpnConnectionState.DISCONNECTED))
        assertEquals("stop", AutomationSemantics.connectionAction(VpnConnectionState.CONNECTING))
        assertEquals("stop", AutomationSemantics.connectionAction(VpnConnectionState.CONNECTED))
        assertEquals("stopping", AutomationSemantics.connectionAction(VpnConnectionState.STOPPING))
    }
}
