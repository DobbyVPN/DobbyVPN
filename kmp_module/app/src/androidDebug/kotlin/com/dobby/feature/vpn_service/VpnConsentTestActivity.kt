package com.dobby.feature.vpn_service

import android.app.Activity
import android.net.VpnService
import android.os.Bundle

/**
 * Debug-only foreground host for instrumentation that exercises Android's real VPN consent UI.
 * Release APKs do not contain this activity.
 */
class VpnConsentTestActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val consentIntent = VpnService.prepare(this)
        if (consentIntent == null) {
            finish()
        } else {
            startActivityForResult(consentIntent, VPN_CONSENT_REQUEST)
        }
    }

    @Suppress("DEPRECATION")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: android.content.Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == VPN_CONSENT_REQUEST) finish()
    }

    private companion object {
        private const val VPN_CONSENT_REQUEST = 1
    }
}
